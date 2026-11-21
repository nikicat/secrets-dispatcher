package gpgsign

import (
	"bufio"
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// What the approval prompt shows about a signed git object. Only the fields of
// its Kind are set.
type SignedPayload struct {
	Kind approval.GPGSignKind
	// Signer is the identity in the payload: a commit's author, a tag's tagger,
	// or a push certificate's pusher.
	Signer string
	// Committer is the commit committer (commit only).
	Committer string
	// Message is the commit message, the tag message, or — for a push
	// certificate — the ref-update lines being pushed.
	Message string
	// ParentHash is the commit's first parent (commit only).
	ParentHash string
	// TagName and Target describe an annotated tag: the tag's name and the hash
	// of the object it points at (tag only).
	TagName string
	Target  string
	// Pushee is the destination URL of a signed push (push only).
	Pushee string
}

// Replaces the fields of info that the signed bytes determine, so the prompt
// shows what the daemon signs (ADR 0008). Returns true if any field changed.
// RepoName and ChangedFiles stay as the client sent them.
func (p SignedPayload) BindDisplay(info *approval.GPGSignInfo) bool {
	changes := []bool{
		set(&info.Kind, p.Kind),
		set(&info.Author, p.Signer),
		set(&info.Committer, p.Committer),
		set(&info.CommitMsg, p.Message),
		set(&info.ParentHash, p.ParentHash),
		set(&info.TagName, p.TagName),
		set(&info.Target, p.Target),
		set(&info.Pushee, p.Pushee),
	}
	return slices.Contains(changes, true)
}

func set[T comparable](dst *T, v T) bool {
	changed := *dst != v
	*dst = v
	return changed
}

// The longest line of a signed object that ParseSignedPayload reads.
const maxSignedLine = 1 << 20

// Returns the kind of git object in data and the fields that the approval
// prompt shows. Data that gitformat-signature(5) does not describe gets Kind
// GPGSignUnknown. Errors when a line of data is longer than maxSignedLine.
func ParseSignedPayload(data []byte) (SignedPayload, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), maxSignedLine)

	var headers, bodyLines []string
	inBody := false
	for scanner.Scan() {
		line := scanner.Text()
		if inBody {
			bodyLines = append(bodyLines, line)
			continue
		}
		if line == "" {
			inBody = true
			continue
		}
		headers = append(headers, line)
	}
	if err := scanner.Err(); err != nil {
		return SignedPayload{}, fmt.Errorf("signed object has a line longer than %d bytes: %w", maxSignedLine, err)
	}

	p := SignedPayload{
		Kind:    approval.GPGSignUnknown,
		Message: strings.TrimRight(strings.Join(bodyLines, "\n"), "\n"),
	}
	if len(headers) == 0 {
		return p, nil
	}

	switch {
	case strings.HasPrefix(headers[0], "tree "):
		p.Kind = approval.GPGSignCommit
	case strings.HasPrefix(headers[0], "object "):
		p.Kind = approval.GPGSignTag
	case strings.HasPrefix(headers[0], "certificate version "):
		p.Kind = approval.GPGSignPush
	}

	for _, h := range headers {
		switch {
		case strings.HasPrefix(h, "author "): // commit
			p.Signer = strings.TrimPrefix(h, "author ")
		case strings.HasPrefix(h, "tagger "): // tag
			p.Signer = strings.TrimPrefix(h, "tagger ")
		case strings.HasPrefix(h, "pusher "): // push certificate
			p.Signer = strings.TrimPrefix(h, "pusher ")
		case strings.HasPrefix(h, "committer "):
			p.Committer = strings.TrimPrefix(h, "committer ")
		case strings.HasPrefix(h, "parent ") && p.ParentHash == "":
			// First parent only (merge commits list several).
			p.ParentHash = strings.TrimPrefix(h, "parent ")
		case strings.HasPrefix(h, "tag "):
			p.TagName = strings.TrimPrefix(h, "tag ")
		case strings.HasPrefix(h, "object ") && p.Target == "":
			p.Target = strings.TrimPrefix(h, "object ")
		case strings.HasPrefix(h, "pushee "):
			p.Pushee = strings.TrimPrefix(h, "pushee ")
		}
	}
	return p, nil
}

// ParseCommitObject parses a raw git commit object and returns the author,
// committer, commit message, and first parent hash. It is a thin wrapper over
// ParseSignedPayload retained for callers and tests that only handle commits;
// for a commit, Signer is the author.
func ParseCommitObject(data []byte) (author, committer, message, parentHash string, err error) {
	p, err := ParseSignedPayload(data)
	return p.Signer, p.Committer, p.Message, p.ParentHash, err
}
