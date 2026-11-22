package approval

import (
	"bufio"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignedObject_Parse(t *testing.T) {
	t.Run("commit", func(t *testing.T) {
		data := []byte("tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
			"parent aaa111\n" +
			"author Alice <alice@example.com> 1771936651 +0200\n" +
			"committer Bob <bob@example.com> 1771936651 +0200\n" +
			"\n" +
			"feat: add files\n")
		p, err := SignedObject(data).Parse()
		require.NoError(t, err)
		assert.Equal(t, SignedPayload{
			Message: "feat: add files",
			Signer:  "Alice <alice@example.com> 1771936651 +0200",
			Details: CommitDetails{Committer: "Bob <bob@example.com> 1771936651 +0200", ParentHash: "aaa111"},
		}, p)
	})

	commit := func(headers, body string) []byte {
		return []byte("tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
			headers +
			"author Alice <alice@example.com> 1771936651 +0200\n" +
			"committer Alice <alice@example.com> 1771936651 +0200\n" +
			"\n" +
			body)
	}

	t.Run("root commit has no parent", func(t *testing.T) {
		p, err := SignedObject(commit("", "feat: add files\n")).Parse()
		require.NoError(t, err)
		assert.Equal(t, CommitDetails{Committer: "Alice <alice@example.com> 1771936651 +0200"}, p.Details)
	})

	t.Run("merge commit shows its first parent", func(t *testing.T) {
		p, err := SignedObject(commit("parent aaa111bbb222\nparent ccc333ddd444\n", "Merge branch 'feature'\n")).Parse()
		require.NoError(t, err)
		assert.Equal(t, CommitDetails{Committer: "Alice <alice@example.com> 1771936651 +0200", ParentHash: "aaa111bbb222"}, p.Details)
	})

	t.Run("commit with empty message", func(t *testing.T) {
		p, err := SignedObject(commit("", "")).Parse()
		require.NoError(t, err)
		assert.Empty(t, p.Message)
	})

	t.Run("multi-line message keeps its lines", func(t *testing.T) {
		p, err := SignedObject(commit("", "feat: big feature\n\nThis is a longer description.\nSpanning multiple lines.\n")).Parse()
		require.NoError(t, err)
		assert.Equal(t, "feat: big feature\n\nThis is a longer description.\nSpanning multiple lines.", p.Message)
	})

	t.Run("annotated tag", func(t *testing.T) {
		data := []byte("object 1111111111111111111111111111111111111111\n" +
			"type commit\n" +
			"tag v1.0.0\n" +
			"tagger Nikolay Bryskin <x@example.com> 1784499962 +0000\n" +
			"\n" +
			"Release 1.0.0\n" +
			"\n" +
			"Highlights here.\n")
		p, err := SignedObject(data).Parse()
		require.NoError(t, err)
		assert.Equal(t, SignedPayload{
			Message: "Release 1.0.0\n\nHighlights here.",
			Signer:  "Nikolay Bryskin <x@example.com> 1784499962 +0000",
			Details: TagDetails{Name: "v1.0.0", Target: "1111111111111111111111111111111111111111"},
		}, p, "tagger is the signer")
	})

	t.Run("signed push certificate", func(t *testing.T) {
		data := []byte("certificate version 0.1\n" +
			"pusher Nikolay Bryskin <x@example.com> 1784499962 +0000\n" +
			"pushee git@github.com:nikicat/secrets-dispatcher.git\n" +
			"nonce 1784499999-abcdef\n" +
			"\n" +
			"aaaa1111 bbbb2222 refs/heads/master\n")
		p, err := SignedObject(data).Parse()
		require.NoError(t, err)
		assert.Equal(t, SignedPayload{
			Message: "aaaa1111 bbbb2222 refs/heads/master",
			Signer:  "Nikolay Bryskin <x@example.com> 1784499962 +0000",
			Details: PushDetails{Pushee: "git@github.com:nikicat/secrets-dispatcher.git"},
		}, p, "pusher is the signer and ref-update lines are the message")
	})

	t.Run("other bytes are refused", func(t *testing.T) {
		_, err := SignedObject("this is not a git object\n").Parse()
		require.ErrorIs(t, err, ErrNotSignable)
	})

	t.Run("line over the bound is refused, not truncated", func(t *testing.T) {
		data := []byte("tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
			"\n" +
			strings.Repeat("a", maxSignedLine+1) + "\n" +
			"trailing line\n")
		_, err := SignedObject(data).Parse()
		require.ErrorIs(t, err, bufio.ErrTooLong)
	})

	t.Run("empty is refused", func(t *testing.T) {
		_, err := SignedObject(nil).Parse()
		require.ErrorIs(t, err, ErrNotSignable)
	})
}
