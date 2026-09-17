package approval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

type legacyManagedRuleFile struct {
	Version int                 `json:"version"`
	Rules   []legacyManagedRule `json:"rules"`
}

type legacyManagedRule struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
	RequestTypes     []string          `json:"request_types"`
	Process          *ProcessMatcher   `json:"process,omitempty"`
	Secret           *SecretMatcher    `json:"secret,omitempty"`
	SearchAttributes map[string]string `json:"search_attributes,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
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

	data, err := io.ReadAll(io.LimitReader(f, managedRuleFileMaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > managedRuleFileMaxSize {
		return nil, fmt.Errorf("managed rule file exceeds %d bytes", managedRuleFileMaxSize)
	}

	// Legacy v1 files did not have an action. Preserve a backup before
	// normalizing them to the expanded managed-rule schema.
	var legacy legacyManagedRuleFile
	if err := decodeManagedRuleJSON(data, &legacy); err == nil && legacy.Version == managedRuleFileVersion {
		migrated, migrateErr := s.migrateLegacy(data)
		if migrateErr != nil {
			return nil, migrateErr
		}
		return migrated, nil
	}

	var file managedRuleFile
	if err := decodeManagedRuleJSON(data, &file); err != nil {
		return nil, fmt.Errorf("decode managed rule file: %w", err)
	}
	if file.Version != managedRuleFileVersion {
		return nil, fmt.Errorf("unsupported managed rule file version %d", file.Version)
	}
	var presence struct {
		Rules []map[string]json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return nil, err
	}
	for i := range file.Rules {
		if i < len(presence.Rules) {
			if _, ok := presence.Rules[i]["enabled"]; !ok {
				file.Rules[i].Enabled = true
			}
		}
		normalizeManagedTrustRule(&file.Rules[i])
	}
	if err := validateManagedRules(file.Rules); err != nil {
		return nil, err
	}
	return cloneManagedTrustRules(file.Rules), nil
}

func decodeManagedRuleJSON(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(dec)
}

func (s *FileManagedTrustRuleStore) migrateLegacy(data []byte) ([]ManagedTrustRule, error) {
	var legacy legacyManagedRuleFile
	if err := decodeManagedRuleJSON(data, &legacy); err != nil {
		return nil, err
	}
	if legacy.Version != managedRuleFileVersion {
		return nil, fmt.Errorf("unsupported legacy managed rule file version %d", legacy.Version)
	}

	migrated := make([]ManagedTrustRule, 0, len(legacy.Rules))
	unsupported := 0
	for _, rule := range legacy.Rules {
		converted, err := migrateLegacyManagedRule(rule)
		if err != nil {
			unsupported++
			slog.Warn("skipping unsupported legacy approval rule", "rule_id", rule.ID, "rule_name", rule.Name, "error", err)
			continue
		}
		migrated = append(migrated, converted)
	}
	if err := validateManagedRules(migrated); err != nil {
		return nil, err
	}

	backupPath := s.path + ".legacy-v1"
	if err := preserveLegacyRuleFile(s.path, backupPath); err != nil {
		return nil, err
	}
	if err := s.Save(migrated); err != nil {
		return nil, err
	}
	slog.Warn("migrated legacy approval rules",
		"migrated", len(migrated),
		"unsupported", unsupported,
		"backup", backupPath)
	return cloneManagedTrustRules(migrated), nil
}

func migrateLegacyManagedRule(rule legacyManagedRule) (ManagedTrustRule, error) {
	converted := ManagedTrustRule{
		ID:        rule.ID,
		Enabled:   rule.Enabled,
		CreatedAt: rule.CreatedAt,
		UpdatedAt: rule.UpdatedAt,
		TrustRule: TrustRule{
			Name:             rule.Name,
			Action:           "approve",
			RequestTypes:     slices.Clone(rule.RequestTypes),
			Process:          rule.Process,
			Secret:           rule.Secret,
			SearchAttributes: cloneStringMap(rule.SearchAttributes),
		},
	}
	normalizeManagedTrustRule(&converted)
	if err := ValidateManagedTrustRule(&converted); err != nil {
		return ManagedTrustRule{}, err
	}
	return converted, nil
}

func preserveLegacyRuleFile(source, backup string) error {
	if err := os.Link(source, backup); err == nil {
		return nil
	} else if !os.IsExist(err) {
		return fmt.Errorf("back up legacy managed rule file: %w", err)
	}
	info, err := os.Lstat(backup)
	if err != nil {
		return err
	}
	return validatePrivateRegularFile(backup, info)
}

// Save atomically replaces the managed rule file with private permissions.
func (s *FileManagedTrustRuleStore) Save(rules []ManagedTrustRule) error {
	rules = cloneManagedTrustRules(rules)
	for i := range rules {
		normalizeManagedTrustRule(&rules[i])
	}
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

// ValidateManagedTrustRule validates durable approve rules. Legacy v1 rules
// intentionally retain broad name/unit/cwd/glob and ancestor matching; newer
// generated rules set Process.Direct for their narrower semantics. GPG signing
// remains excluded because signing requires the specialized resolver path.
func ValidateManagedTrustRule(rule *ManagedTrustRule) error {
	if rule == nil {
		return fmt.Errorf("%w: missing rule", ErrInvalidManagedRule)
	}
	if rule.ID == "" || strings.Contains(rule.ID, "/") {
		return fmt.Errorf("%w: invalid id", ErrInvalidManagedRule)
	}
	if rule.CreatedAt.IsZero() {
		return fmt.Errorf("%w: missing created_at", ErrInvalidManagedRule)
	}
	if rule.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: missing updated_at", ErrInvalidManagedRule)
	}
	if rule.Action == "" {
		rule.Action = "approve"
	}
	if rule.Action != "approve" {
		return fmt.Errorf("%w: action must be approve", ErrInvalidManagedRule)
	}
	if len(rule.RequestTypes) == 0 {
		return fmt.Errorf("%w: request_types must not be empty", ErrInvalidManagedRule)
	}
	for _, requestType := range rule.RequestTypes {
		if !validManagedRequestType(requestType) {
			return fmt.Errorf("%w: request_type %q is not supported", ErrInvalidManagedRule, requestType)
		}
	}
	if !hasManagedProcessMatcher(rule.Process) {
		return fmt.Errorf("%w: process matcher is required", ErrInvalidManagedRule)
	}
	if rule.Process.Direct && rule.Process.Exe != "" && !strings.HasPrefix(rule.Process.Exe, "/") {
		return fmt.Errorf("%w: process executable must be absolute", ErrInvalidManagedRule)
	}

	patterns := []struct {
		name  string
		value string
	}{
		{"process.exe", rule.Process.Exe},
		{"process.name", rule.Process.Name},
		{"process.args", rule.Process.Args},
		{"process.cwd", rule.Process.CWD},
		{"process.unit", rule.Process.Unit},
	}
	if rule.Secret != nil {
		patterns = append(patterns,
			struct{ name, value string }{"secret.collection", rule.Secret.Collection},
			struct{ name, value string }{"secret.label", rule.Secret.Label})
	}
	for _, pattern := range patterns {
		if pattern.value == "" {
			continue
		}
		if _, err := path.Match(pattern.value, "test"); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrInvalidManagedRule, pattern.name, err)
		}
	}
	var secretAttributes map[string]string
	if rule.Secret != nil {
		secretAttributes = rule.Secret.Attributes
	}
	for key, value := range secretAttributes {
		if key == "" {
			return fmt.Errorf("%w: empty secret attribute key", ErrInvalidManagedRule)
		}
		if _, err := path.Match(value, "test"); err != nil {
			return fmt.Errorf("%w: secret.attributes[%s]: %v", ErrInvalidManagedRule, key, err)
		}
	}
	for key, value := range rule.SearchAttributes {
		if key == "" {
			return fmt.Errorf("%w: empty search attribute key", ErrInvalidManagedRule)
		}
		if _, err := path.Match(value, "test"); err != nil {
			return fmt.Errorf("%w: search_attributes[%s]: %v", ErrInvalidManagedRule, key, err)
		}
	}
	return nil
}

func normalizeManagedTrustRule(rule *ManagedTrustRule) {
	if rule.Action == "" {
		rule.Action = "approve"
	}
	if rule.UpdatedAt.IsZero() {
		rule.UpdatedAt = rule.CreatedAt
	}
}

func validManagedRequestType(requestType string) bool {
	switch RequestType(requestType) {
	case RequestTypeGetSecret, RequestTypeSearch, RequestTypeDelete, RequestTypeWrite, RequestTypeUnlock, RequestTypeSSHSign:
		return true
	default:
		return false
	}
}

func hasManagedProcessMatcher(process *ProcessMatcher) bool {
	return process != nil && (process.Exe != "" || process.Name != "" || process.CWD != "" || process.Unit != "")
}

func managedTrustRuleFromRequest(req *Request) (ManagedTrustRule, error) {
	if req == nil || !validManagedRequestType(string(req.Type)) {
		return ManagedTrustRule{}, fmt.Errorf("%w: request type cannot be saved", ErrInvalidManagedRule)
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

	name := filepath.Base(direct.Exe) + " " + string(req.Type)
	if len(req.Items) > 0 {
		if req.Items[0].Label != "" {
			name = filepath.Base(direct.Exe) + ": " + req.Items[0].Label
		} else if collection := extractCollection(req.Items[0].Path); collection != "" {
			name = filepath.Base(direct.Exe) + ": " + collection
		}
	}
	rule := ManagedTrustRule{
		ID:        uuid.New().String(),
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		TrustRule: TrustRule{
			Name:         name,
			Action:       "approve",
			RequestTypes: []string{string(req.Type)},
			Process:      process,
		},
	}
	if req.Type == RequestTypeSearch {
		rule.SearchAttributes = quoteMap(req.SearchAttributes)
	} else if len(req.Items) > 0 {
		item := req.Items[0]
		rule.Secret = &SecretMatcher{Collection: globQuote(extractCollection(item.Path)), Attributes: quoteMap(item.Attributes)}
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

func managedTrustRulesEqual(a, b ManagedTrustRule) bool {
	return a.Enabled == b.Enabled &&
		slices.Equal(a.RequestTypes, b.RequestTypes) &&
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
