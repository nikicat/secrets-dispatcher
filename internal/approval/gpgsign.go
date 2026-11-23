package approval

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const RequestTypeGPGSign RequestType = "gpg_sign"

// The kind of git object that a gpg_sign request signs. git hands commits,
// annotated tags and push certificates to the same gpg.program, so the signed
// bytes are not always a commit (ADR 0008).
type GPGSignKind string

const (
	GPGSignCommit GPGSignKind = "commit"
	GPGSignTag    GPGSignKind = "tag"
	GPGSignPush   GPGSignKind = "push"
)

// The key that git names for gpg's -u option.
type GPGKeyID string

// The hex hash that names a git object.
type ObjectHash string

type TagName string

// A git remote as a pushee header spells it, such as git@host:repo.git.
type RemoteURL string

// What a signed object's bytes say about it, and a commit's staged files.
type SignedPayload struct {
	// A commit's or a tag's message, or the ref updates of a push certificate.
	Message string
	// A commit's author, a tag's tagger, or a push certificate's pusher.
	Signer string
	// The fields of the object's kind.
	Details ObjectDetails
}

// The fields that only one kind of signed object has.
type ObjectDetails interface {
	Kind() GPGSignKind
	toWire(*gpgSignInfoWire)
	// Returns the staged files and true for a commit, and false for every
	// other kind.
	changedFiles() ([]string, bool)
	withChangedFiles(files []string) ObjectDetails
}

type CommitDetails struct {
	Committer string
	// The first parent. A merge commit has more.
	ParentHash ObjectHash
	// The files staged in the working tree, relative to its top level. The
	// signed bytes do not hold them (ADR 0008).
	ChangedFiles []string
}

type TagDetails struct {
	Name TagName
	// The object that the tag points at.
	Target ObjectHash
}

type PushDetails struct {
	// The URL that the push certificate pushes to.
	Pushee RemoteURL
}

func (CommitDetails) Kind() GPGSignKind { return GPGSignCommit }
func (TagDetails) Kind() GPGSignKind    { return GPGSignTag }
func (PushDetails) Kind() GPGSignKind   { return GPGSignPush }

func (d CommitDetails) toWire(w *gpgSignInfoWire) {
	w.Committer, w.ParentHash, w.ChangedFiles = d.Committer, d.ParentHash, d.ChangedFiles
}
func (d TagDetails) toWire(w *gpgSignInfoWire)  { w.TagName, w.Target = d.Name, d.Target }
func (d PushDetails) toWire(w *gpgSignInfoWire) { w.Pushee = d.Pushee }

func (d CommitDetails) changedFiles() ([]string, bool) { return d.ChangedFiles, true }
func (TagDetails) changedFiles() ([]string, bool)      { return nil, false }
func (PushDetails) changedFiles() ([]string, bool)     { return nil, false }

func (d CommitDetails) withChangedFiles(files []string) ObjectDetails {
	d.ChangedFiles = files
	return d
}
func (d TagDetails) withChangedFiles([]string) ObjectDetails  { return d }
func (d PushDetails) withChangedFiles([]string) ObjectDetails { return d }

// Lists the files staged in the working tree, as CommitDetails.ChangedFiles
// holds them.
type StagedFiles interface {
	List() ([]string, error)
}

// Staged files that are already listed.
type ListedFiles []string

func (f ListedFiles) List() ([]string, error) { return f, nil }

// A gpg_sign request's object and its context. RepoName describes the working
// tree, not the signed object (ADR 0008).
type GPGSignInfo struct {
	RepoName RepoName
	KeyID    GPGKeyID
	object   SignedObject
	signed   SignedPayload
}

// Returns the bytes that gpg signs.
func (i *GPGSignInfo) SignedObject() SignedObject { return i.object }

// Returns what SignedObject says.
func (i *GPGSignInfo) Signed() SignedPayload { return i.signed }

// Returns the sign info of the given object, with the display fields read from
// its bytes. Only a commit gets the given staged files. Errors as
// SignedObject.Parse does, and when a commit's staged files cannot be listed.
func NewGPGSignInfo(object SignedObject, repo RepoName, key GPGKeyID, staged StagedFiles) (*GPGSignInfo, error) {
	signed, err := object.Parse()
	if err != nil {
		return nil, err
	}
	if _, commit := signed.Details.changedFiles(); commit {
		files, err := staged.List()
		if err != nil {
			return nil, fmt.Errorf("list the staged files: %w", err)
		}
		signed.Details = signed.Details.withChangedFiles(files)
	}
	return &GPGSignInfo{RepoName: repo, KeyID: key, object: object, signed: signed}, nil
}

// The JSON form of GPGSignInfo. Its display keys keep their commit names for
// every kind.
type gpgSignInfoWire struct {
	RepoName     RepoName    `json:"repo_name"`
	Kind         GPGSignKind `json:"kind"`
	CommitMsg    string      `json:"commit_msg"`
	Author       string      `json:"author"`
	Committer    string      `json:"committer"`
	KeyID        GPGKeyID    `json:"key_id"`
	ChangedFiles []string    `json:"changed_files"`
	ParentHash   ObjectHash  `json:"parent_hash,omitempty"`
	TagName      TagName     `json:"tag_name,omitempty"`
	Target       ObjectHash  `json:"target,omitempty"`
	Pushee       RemoteURL   `json:"pushee,omitempty"`
	CommitObject string      `json:"commit_object,omitempty"`
}

// Errors when SignedObject is not valid UTF-8, since commit_object is a JSON
// string and cannot carry such bytes unchanged.
func (i GPGSignInfo) MarshalJSON() ([]byte, error) {
	if !utf8.Valid(i.object) {
		return nil, errors.New("gpg sign info: the object to sign is not valid UTF-8, and the daemon API carries it as a JSON string")
	}
	w := gpgSignInfoWire{
		RepoName:     i.RepoName,
		Kind:         i.signed.Details.Kind(),
		CommitMsg:    i.signed.Message,
		Author:       i.signed.Signer,
		KeyID:        i.KeyID,
		CommitObject: string(i.object),
	}
	i.signed.Details.toWire(&w)
	return json.Marshal(w)
}

// Reads the display fields from commit_object, not from the client's copies, so
// the prompt shows what the daemon signs (ADR 0008). Errors when commit_object
// is missing, or as NewGPGSignInfo does.
func (i *GPGSignInfo) UnmarshalJSON(data []byte) error {
	var w gpgSignInfoWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	if w.CommitObject == "" {
		return errors.New("gpg sign info: commit_object is required")
	}
	info, err := NewGPGSignInfo(SignedObject(w.CommitObject), w.RepoName, w.KeyID, ListedFiles(w.ChangedFiles))
	if err != nil {
		return fmt.Errorf("gpg sign info: %w", err)
	}
	*i = *info
	return nil
}

// What real gpg produced for one approved gpg_sign request.
type GPGResult struct {
	Signature []byte `json:"signature,omitempty"`
	Status    []byte `json:"gpg_status,omitempty"`
	// Sent even when zero, so that a result with no signature and no status
	// still decodes as a result.
	ExitCode GPGExitCode `json:"exit_code"`
}

// Checks whether gpg ran and produced a signature.
func (r GPGResult) Succeeded() bool { return r.ExitCode == 0 }

// The exit code of gpg for one sign request. It is the gpg process's own code,
// or one of the codes below when gpg did not run.
type GPGExitCode int

// Exit codes the daemon reports when it could not run gpg.
const (
	// gpg was found but did not start.
	GPGNotStarted GPGExitCode = 1
	// No gpg binary was found.
	GPGNotFound GPGExitCode = 2
)

// Returns what gpg produced for an approved or auto-approved gpg_sign request,
// and nil for any other request or resolution.
func (e HistoryEntry) GPGOutput() *GPGResult {
	if e.Request.Type != RequestTypeGPGSign || !e.Resolution.Approves() {
		return nil
	}
	return &e.Request.GPGResult
}

// Returns the gpg_sign request that the given sender makes to sign this object.
// The request's one item is labelled with the repository name and the first
// line of the object's message.
func (i *GPGSignInfo) RequestInfo(sender SenderInfo) RequestInfo {
	label := string(i.RepoName)
	if i.signed.Message != "" {
		subject := i.signed.Message
		if len(subject) > 60 {
			subject = subject[:60]
		}
		if nl := strings.IndexByte(subject, '\n'); nl >= 0 {
			subject = subject[:nl]
		}
		label += ": " + subject
	}
	return RequestInfo{
		Type:       RequestTypeGPGSign,
		Items:      []ItemInfo{{Label: label}},
		SenderInfo: sender,
		Payload:    i,
	}
}

// Records the gpg_sign request as approved without a prompt, and stores the
// given gpg result on it. Returns the request's ID.
func (m *Manager) RecordAutoApprovedGPGSign(info RequestInfo, res GPGResult) RequestID {
	req := info.newRecord()
	req.GPGResult = res
	m.publishEnd(req.historyEntry(ResolutionAutoApproved))
	return req.ID
}
