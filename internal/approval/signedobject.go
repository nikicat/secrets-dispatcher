package approval

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// The bytes that gpg signs: a commit, an annotated tag or a push certificate
// (ADR 0008).
type SignedObject []byte

// Reports bytes that are not one of the kinds of object git signs, so the
// approver could not be shown what they sign (ADR 0008).
var ErrNotSignable = errors.New("signed object is not a commit, an annotated tag or a push certificate")

// The longest line of a signed object that Parse reads.
const maxSignedLine = 1 << 20

// Errors with ErrNotSignable when the bytes are not a commit, an annotated tag or
// a push certificate, and with another error when a line is longer than
// maxSignedLine.
func (o SignedObject) Parse() (SignedPayload, error) {
	scanner := bufio.NewScanner(bytes.NewReader(o))
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

	p := SignedPayload{Message: strings.TrimRight(strings.Join(bodyLines, "\n"), "\n")}
	if len(headers) == 0 {
		return SignedPayload{}, ErrNotSignable
	}

	var (
		commit CommitDetails
		tag    TagDetails
		push   PushDetails
	)
	for _, h := range headers {
		key, value, _ := strings.Cut(h, " ")
		switch key {
		case "object":
			if tag.Target == "" {
				tag.Target = ObjectHash(value)
			}
		case "author", "tagger", "pusher":
			p.Signer = value
		case "committer":
			commit.Committer = value
		case "parent":
			if commit.ParentHash == "" {
				commit.ParentHash = ObjectHash(value)
			}
		case "tag":
			tag.Name = TagName(value)
		case "pushee":
			push.Pushee = RemoteURL(value)
		}
	}

	// The first header names the kind of object (gitformat-signature(5)).
	switch key, value, _ := strings.Cut(headers[0], " "); {
	case key == "tree":
		p.Details = commit
	case key == "object":
		p.Details = tag
	case key == "certificate" && strings.HasPrefix(value, "version "):
		p.Details = push
	default:
		return SignedPayload{}, ErrNotSignable
	}
	return p, nil
}
