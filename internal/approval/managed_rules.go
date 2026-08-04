package approval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	managedRuleFileVersion = 1
	managedRuleFileMaxSize = 1 << 20
	managedRuleMaxCount    = 256
)

// ErrInvalidManagedRule is returned when a managed trust rule is unsafe or malformed.
var ErrInvalidManagedRule = errors.New("invalid managed trust rule")

// ManagedTrustRule adds persistence metadata to the existing TrustRule policy model.
type ManagedTrustRule struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	TrustRule
}

// ManagedTrustRuleStore persists user-managed trust rules.
type ManagedTrustRuleStore interface {
	Load() ([]ManagedTrustRule, error)
	Save([]ManagedTrustRule) error
}

// FileManagedTrustRuleStore stores managed rules in the daemon state directory.
type FileManagedTrustRuleStore struct {
	path string
}

type managedRuleFile struct {
	Version int                `json:"version"`
	Rules   []ManagedTrustRule `json:"rules"`
}

// NewFileManagedTrustRuleStore creates a versioned JSON rule store.
func NewFileManagedTrustRuleStore(stateDir string) *FileManagedTrustRuleStore {
	return &FileManagedTrustRuleStore{path: filepath.Join(stateDir, "approval-rules.json")}
}

// Load reads and validates all managed rules. A missing file means no rules.
func (s *FileManagedTrustRuleStore) Load() ([]ManagedTrustRule, error) {
	info, err := os.Lstat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	dirInfo, err := os.Lstat(filepath.Dir(s.path))
	if err != nil {
		return nil, err
	}
	if err := validatePrivateDir(filepath.Dir(s.path), dirInfo); err != nil {
		return nil, err
	}
	if err := validatePrivateRegularFile(s.path, info); err != nil {
		return nil, err
	}
	if info.Size() > managedRuleFileMaxSize {
		return nil, fmt.Errorf("managed rule file exceeds %d bytes", managedRuleFileMaxSize)
	}

	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := json.NewDecoder(io.LimitReader(f, managedRuleFileMaxSize+1))
	dec.DisallowUnknownFields()
	var file managedRuleFile
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("decode managed rule file: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return nil, err
	}
	if file.Version != managedRuleFileVersion {
		return nil, fmt.Errorf("unsupported managed rule file version %d", file.Version)
	}
	if err := validateManagedRules(file.Rules); err != nil {
		return nil, err
	}
	return cloneManagedTrustRules(file.Rules), nil
}

// Save atomically replaces the managed rule file with private permissions.
func (s *FileManagedTrustRuleStore) Save(rules []ManagedTrustRule) error {
	if err := validateManagedRules(rules); err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}

	data, err := json.MarshalIndent(managedRuleFile{Version: managedRuleFileVersion, Rules: rules}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > managedRuleFileMaxSize {
		return fmt.Errorf("managed rule file exceeds %d bytes", managedRuleFileMaxSize)
	}

	tmp, err := os.CreateTemp(dir, ".approval-rules-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck

	fail := func(err error) error {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	closeErr := d.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("managed rule directory is not a real directory: %s", dir)
	}
	if err := checkOwner(info); err != nil {
		return fmt.Errorf("managed rule directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	return nil
}

func validatePrivateDir(name string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("managed rule directory is not a real directory: %s", name)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("managed rule directory permissions must be 0700: %s", name)
	}
	if err := checkOwner(info); err != nil {
		return fmt.Errorf("managed rule directory %s: %w", name, err)
	}
	return nil
}

func validatePrivateRegularFile(name string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("managed rule file is not a regular file: %s", name)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("managed rule file permissions must be 0600: %s", name)
	}
	if err := checkOwner(info); err != nil {
		return fmt.Errorf("managed rule file %s: %w", name, err)
	}
	return nil
}

func checkOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("owner UID %d does not match effective UID %d", stat.Uid, os.Geteuid())
	}
	return nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("managed rule file contains multiple JSON values")
		}
		return fmt.Errorf("decode managed rule file: %w", err)
	}
	return nil
}

func validateManagedRules(rules []ManagedTrustRule) error {
	if len(rules) > managedRuleMaxCount {
		return fmt.Errorf("managed rule count exceeds %d", managedRuleMaxCount)
	}
	seen := make(map[string]struct{}, len(rules))
	for i := range rules {
		if err := ValidateManagedTrustRule(&rules[i]); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
		if _, ok := seen[rules[i].ID]; ok {
			return fmt.Errorf("rules[%d]: duplicate rule ID %q", i, rules[i].ID)
		}
		seen[rules[i].ID] = struct{}{}
	}
	return nil
}

// ValidateManagedTrustRule enforces the deliberately narrow managed-rule policy.
func ValidateManagedTrustRule(rule *ManagedTrustRule) error {
	if rule == nil {
		return fmt.Errorf("%w: missing rule", ErrInvalidManagedRule)
	}
	if _, err := uuid.Parse(rule.ID); err != nil {
		return fmt.Errorf("%w: invalid id", ErrInvalidManagedRule)
	}
	if rule.CreatedAt.IsZero() {
		return fmt.Errorf("%w: missing created_at", ErrInvalidManagedRule)
	}
	if rule.Action != "approve" {
		return fmt.Errorf("%w: action must be approve", ErrInvalidManagedRule)
	}
	if len(rule.RequestTypes) != 1 || rule.RequestTypes[0] != string(RequestTypeGetSecret) {
		return fmt.Errorf("%w: only get_secret is supported", ErrInvalidManagedRule)
	}
	if len(rule.SearchAttributes) != 0 {
		return fmt.Errorf("%w: search_attributes are not supported", ErrInvalidManagedRule)
	}
	if rule.Process == nil || !rule.Process.Direct || rule.Process.Exe == "" {
		return fmt.Errorf("%w: direct executable matcher is required", ErrInvalidManagedRule)
	}
	if rule.Process.Name != "" || rule.Process.Unit != "" {
		return fmt.Errorf("%w: process name and unit are not supported", ErrInvalidManagedRule)
	}
	if !strings.HasPrefix(rule.Process.Exe, "/") {
		return fmt.Errorf("%w: process executable must be absolute", ErrInvalidManagedRule)
	}
	if rule.Secret == nil || rule.Secret.Collection == "" || len(rule.Secret.Attributes) == 0 {
		return fmt.Errorf("%w: collection and attributes are required", ErrInvalidManagedRule)
	}
	if rule.Secret.Label != "" {
		return fmt.Errorf("%w: label matching is not supported", ErrInvalidManagedRule)
	}

	patterns := []struct {
		name  string
		value string
	}{
		{"process.exe", rule.Process.Exe},
		{"process.args", rule.Process.Args},
		{"process.cwd", rule.Process.CWD},
		{"secret.collection", rule.Secret.Collection},
	}
	for _, pattern := range patterns {
		if pattern.value == "" {
			continue
		}
		if err := validateLiteralPattern(pattern.value); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidManagedRule, pattern.name, err)
		}
	}
	for key, value := range rule.Secret.Attributes {
		if key == "" {
			return fmt.Errorf("%w: empty secret attribute key", ErrInvalidManagedRule)
		}
		if err := validateLiteralPattern(value); err != nil {
			return fmt.Errorf("%w: secret.attributes[%s]: %v", ErrInvalidManagedRule, key, err)
		}
	}
	return nil
}

func validateLiteralPattern(pattern string) error {
	if _, err := path.Match(pattern, "test"); err != nil {
		return fmt.Errorf("invalid pattern: %w", err)
	}
	escaped := false
	for _, r := range pattern {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '*' || r == '?' || r == '[' {
			return errors.New("wildcards are not allowed")
		}
	}
	if escaped {
		return errors.New("trailing escape")
	}
	return nil
}

func managedTrustRuleFromRequest(req *Request) (ManagedTrustRule, error) {
	if req == nil || req.Type != RequestTypeGetSecret {
		return ManagedTrustRule{}, fmt.Errorf("%w: only get_secret requests can be saved", ErrInvalidManagedRule)
	}
	if len(req.Items) != 1 {
		return ManagedTrustRule{}, fmt.Errorf("%w: exactly one item is required", ErrInvalidManagedRule)
	}
	item := req.Items[0]
	collection := extractCollection(item.Path)
	if collection == "" || len(item.Attributes) == 0 {
		return ManagedTrustRule{}, fmt.Errorf("%w: item collection and attributes are required", ErrInvalidManagedRule)
	}
	if len(req.SenderInfo.ProcessChain) == 0 {
		return ManagedTrustRule{}, fmt.Errorf("%w: direct process information is required", ErrInvalidManagedRule)
	}
	direct := req.SenderInfo.ProcessChain[0]
	if direct.Exe == "" || !filepath.IsAbs(direct.Exe) {
		return ManagedTrustRule{}, fmt.Errorf("%w: direct executable must be resolved and absolute", ErrInvalidManagedRule)
	}

	process := &ProcessMatcher{Exe: globQuote(direct.Exe), Direct: true}
	if isInterpreter(direct.Exe) {
		script, err := interpreterScriptArg(direct.Args)
		if err != nil {
			return ManagedTrustRule{}, fmt.Errorf("%w: %v", ErrInvalidManagedRule, err)
		}
		process.Args = globQuote(script)
		if !filepath.IsAbs(script) {
			if direct.CWD == "" || !filepath.IsAbs(direct.CWD) {
				return ManagedTrustRule{}, fmt.Errorf("%w: relative interpreter script requires an absolute cwd", ErrInvalidManagedRule)
			}
			process.CWD = globQuote(direct.CWD)
		}
	}

	name := filepath.Base(direct.Exe) + ": " + item.Label
	if item.Label == "" {
		name = filepath.Base(direct.Exe) + ": " + collection
	}
	rule := ManagedTrustRule{
		ID:        uuid.New().String(),
		CreatedAt: time.Now().UTC(),
		TrustRule: TrustRule{
			Name:         name,
			Action:       "approve",
			RequestTypes: []string{string(RequestTypeGetSecret)},
			Process:      process,
			Secret: &SecretMatcher{
				Collection: globQuote(collection),
				Attributes: quoteMap(item.Attributes),
			},
		},
	}
	if err := ValidateManagedTrustRule(&rule); err != nil {
		return ManagedTrustRule{}, err
	}
	return rule, nil
}

func isInterpreter(exe string) bool {
	base := strings.ToLower(filepath.Base(exe))
	if strings.HasPrefix(base, "python") {
		return true
	}
	switch base {
	case "sh", "bash", "dash", "zsh", "fish", "ksh", "csh", "tcsh", "node", "nodejs", "ruby", "perl":
		return true
	default:
		return false
	}
}

func interpreterScriptArg(args []string) (string, error) {
	if len(args) < 2 {
		return "", errors.New("interpreter invocation has no script argument")
	}
	if args[1] == "--" {
		if len(args) < 3 || args[2] == "" {
			return "", errors.New("interpreter invocation has no script argument")
		}
		return args[2], nil
	}
	if args[1] == "" || strings.HasPrefix(args[1], "-") {
		return "", errors.New("interpreter options, modules, eval, and stdin are not supported")
	}
	return args[1], nil
}

func globQuote(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '\\', '*', '?', '[':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func quoteMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	quoted := make(map[string]string, len(values))
	for key, value := range values {
		quoted[key] = globQuote(value)
	}
	return quoted
}

func cloneManagedTrustRules(rules []ManagedTrustRule) []ManagedTrustRule {
	if rules == nil {
		return nil
	}
	cloned := make([]ManagedTrustRule, len(rules))
	for i := range rules {
		cloned[i] = cloneManagedTrustRule(rules[i])
	}
	return cloned
}

func cloneManagedTrustRule(rule ManagedTrustRule) ManagedTrustRule {
	rule.RequestTypes = slices.Clone(rule.RequestTypes)
	rule.SearchAttributes = cloneStringMap(rule.SearchAttributes)
	if rule.Process != nil {
		process := *rule.Process
		rule.Process = &process
	}
	if rule.Secret != nil {
		secret := *rule.Secret
		secret.Attributes = cloneStringMap(rule.Secret.Attributes)
		rule.Secret = &secret
	}
	return rule
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func managedTrustRulesEqual(a, b ManagedTrustRule) bool {
	return slices.Equal(a.RequestTypes, b.RequestTypes) &&
		processMatchersEqual(a.Process, b.Process) &&
		secretMatchersEqual(a.Secret, b.Secret) &&
		mapsEqual(a.SearchAttributes, b.SearchAttributes) &&
		a.Action == b.Action
}

func processMatchersEqual(a, b *ProcessMatcher) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func secretMatchersEqual(a, b *SecretMatcher) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Collection == b.Collection && a.Label == b.Label && mapsEqual(a.Attributes, b.Attributes)
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
