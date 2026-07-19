package gpgsign

import (
	"bufio"
	"bytes"
	"strings"
)

// PayloadKind identifies what kind of git object the signer was asked to sign.
// git routes commits, annotated tags, and signed-push certificates through the
// same signing invocation (--status-fd=2 -bsau <key>), so the raw bytes on
// stdin are not always a commit.
type PayloadKind string

const (
	KindCommit  PayloadKind = "commit"
	KindTag     PayloadKind = "tag"
	KindPush    PayloadKind = "push"
	KindUnknown PayloadKind = "unknown"
)

// SignedPayload is the parsed, human-displayable context of the bytes git asked
// us to sign. Only the fields relevant to Kind are populated; the rest are
// empty. It backs the approval prompt so a user can see *what* they are signing
// — a commit, a release tag, or a push to a specific remote.
type SignedPayload struct {
	Kind PayloadKind
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

// ParseSignedPayload detects the kind of git object in data and extracts the
// fields worth showing in the approval prompt.
//
// The three payloads git may feed to gpg.program share a "headers, blank line,
// body" shape but differ in their leading header:
//
//	commit:  tree <sha> / parent* / author <id> / committer <id>  \n\n <message>
//	tag:     object <sha> / type <t> / tag <name> / tagger <id>    \n\n <message>
//	push:    certificate version <v> / pusher <id> / pushee <url>  \n\n <ref updates>
//	         / nonce <n> / push-option*
func ParseSignedPayload(data []byte) SignedPayload {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Commit/tag messages and push certificates are small, but a single long
	// line (e.g. a paragraph-per-line message) can exceed the 64KB default and
	// silently truncate the scan; size the buffer to the daemon's 1MB payload
	// limit so parsing never loses trailing headers/body.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

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

	p := SignedPayload{
		Kind:    KindUnknown,
		Message: strings.TrimRight(strings.Join(bodyLines, "\n"), "\n"),
	}
	if len(headers) == 0 {
		return p
	}

	switch {
	case strings.HasPrefix(headers[0], "tree "):
		p.Kind = KindCommit
	case strings.HasPrefix(headers[0], "object "):
		p.Kind = KindTag
	case strings.HasPrefix(headers[0], "certificate version "):
		p.Kind = KindPush
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
	return p
}

// ParseCommitObject parses a raw git commit object and returns the author,
// committer, commit message, and first parent hash. It is a thin wrapper over
// ParseSignedPayload retained for callers and tests that only handle commits;
// for a commit, Signer is the author.
func ParseCommitObject(data []byte) (author, committer, message, parentHash string) {
	p := ParseSignedPayload(data)
	return p.Signer, p.Committer, p.Message, p.ParentHash
}
