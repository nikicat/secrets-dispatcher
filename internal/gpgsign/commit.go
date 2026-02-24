package gpgsign

import (
	"bufio"
	"bytes"
	"strings"
)

// ParseCommitObject parses a raw git commit object and extracts the author,
// committer, commit message, and first parent hash.
//
// The commit object format is:
//
//	tree <sha>
//	parent <sha>        (optional, may repeat for merge commits)
//	author <identity>
//	committer <identity>
//	                    (blank line)
//	<commit message>
//
// For merge commits with multiple parent lines, the first parent hash is
// returned. The trailing newline that git appends to the commit message body
// is stripped.
func ParseCommitObject(data []byte) (author, committer, message, parentHash string) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var bodyLines []string
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
		switch {
		case strings.HasPrefix(line, "author "):
			author = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "committer "):
			committer = strings.TrimPrefix(line, "committer ")
		case strings.HasPrefix(line, "parent ") && parentHash == "":
			// Only store the first parent (merge commits have multiple parent lines).
			parentHash = strings.TrimPrefix(line, "parent ")
		}
	}

	// Join body lines and strip the trailing newline that git appends.
	message = strings.TrimRight(strings.Join(bodyLines, "\n"), "\n")
	return
}
