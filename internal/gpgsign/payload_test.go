package gpgsign

import (
	"bufio"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

func TestParseSignedPayload(t *testing.T) {
	t.Run("commit", func(t *testing.T) {
		data := []byte("tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
			"parent aaa111\n" +
			"author Alice <alice@example.com> 1771936651 +0200\n" +
			"committer Bob <bob@example.com> 1771936651 +0200\n" +
			"\n" +
			"feat: add files\n")
		p, err := ParseSignedPayload(data)
		require.NoError(t, err)
		assert.Equal(t, approval.GPGSignCommit, p.Kind)
		assert.Equal(t, "Alice <alice@example.com> 1771936651 +0200", p.Signer)
		assert.Equal(t, "Bob <bob@example.com> 1771936651 +0200", p.Committer)
		assert.Equal(t, "aaa111", p.ParentHash)
		assert.Equal(t, "feat: add files", p.Message)
		assert.Empty(t, p.TagName)
		assert.Empty(t, p.Pushee)
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
		p, err := ParseSignedPayload(data)
		require.NoError(t, err)
		assert.Equal(t, approval.GPGSignTag, p.Kind)
		assert.Equal(t, "Nikolay Bryskin <x@example.com> 1784499962 +0000", p.Signer, "tagger is the signer")
		assert.Equal(t, "v1.0.0", p.TagName)
		assert.Equal(t, "1111111111111111111111111111111111111111", p.Target)
		assert.Equal(t, "Release 1.0.0\n\nHighlights here.", p.Message)
		// Commit-only fields stay empty for a tag — this is the bug being fixed
		// (previously the tag was parsed as a commit and everything blanked).
		assert.Empty(t, p.Committer)
		assert.Empty(t, p.ParentHash)
	})

	t.Run("signed push certificate", func(t *testing.T) {
		data := []byte("certificate version 0.1\n" +
			"pusher Nikolay Bryskin <x@example.com> 1784499962 +0000\n" +
			"pushee git@github.com:nikicat/secrets-dispatcher.git\n" +
			"nonce 1784499999-abcdef\n" +
			"\n" +
			"aaaa1111 bbbb2222 refs/heads/master\n")
		p, err := ParseSignedPayload(data)
		require.NoError(t, err)
		assert.Equal(t, approval.GPGSignPush, p.Kind)
		assert.Equal(t, "Nikolay Bryskin <x@example.com> 1784499962 +0000", p.Signer, "pusher is the signer")
		assert.Equal(t, "git@github.com:nikicat/secrets-dispatcher.git", p.Pushee)
		assert.Equal(t, "aaaa1111 bbbb2222 refs/heads/master", p.Message, "ref-update lines are the message")
		assert.Empty(t, p.Committer)
		assert.Empty(t, p.TagName)
	})

	t.Run("unknown payload", func(t *testing.T) {
		p, err := ParseSignedPayload([]byte("this is not a git object\n"))
		require.NoError(t, err)
		assert.Equal(t, approval.GPGSignUnknown, p.Kind)
	})

	t.Run("line over the bound is refused, not truncated", func(t *testing.T) {
		data := []byte("tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
			"\n" +
			strings.Repeat("a", maxSignedLine+1) + "\n" +
			"trailing line\n")
		_, err := ParseSignedPayload(data)
		require.ErrorIs(t, err, bufio.ErrTooLong)
	})

	t.Run("empty", func(t *testing.T) {
		p, err := ParseSignedPayload(nil)
		require.NoError(t, err)
		assert.Equal(t, approval.GPGSignUnknown, p.Kind)
	})
}

func TestSignedPayloadBindDisplay(t *testing.T) {
	p := SignedPayload{
		Kind:       approval.GPGSignTag,
		Signer:     "signer",
		Committer:  "committer",
		Message:    "message",
		ParentHash: "parent",
		TagName:    "v1",
		Target:     "target",
		Pushee:     "pushee",
	}
	bound := func() approval.GPGSignInfo {
		return approval.GPGSignInfo{
			RepoName:     "repo",
			ChangedFiles: []string{"a.go"},
			Kind:         p.Kind,
			Author:       p.Signer,
			Committer:    p.Committer,
			CommitMsg:    p.Message,
			ParentHash:   p.ParentHash,
			TagName:      p.TagName,
			Target:       p.Target,
			Pushee:       p.Pushee,
		}
	}

	t.Run("all agree", func(t *testing.T) {
		info := bound()
		assert.False(t, p.BindDisplay(&info))
		assert.Equal(t, bound(), info)
	})

	lies := map[string]func(*approval.GPGSignInfo){
		"Kind":       func(i *approval.GPGSignInfo) { i.Kind = approval.GPGSignPush },
		"Author":     func(i *approval.GPGSignInfo) { i.Author = "lie" },
		"Committer":  func(i *approval.GPGSignInfo) { i.Committer = "lie" },
		"CommitMsg":  func(i *approval.GPGSignInfo) { i.CommitMsg = "lie" },
		"ParentHash": func(i *approval.GPGSignInfo) { i.ParentHash = "lie" },
		"TagName":    func(i *approval.GPGSignInfo) { i.TagName = "lie" },
		"Target":     func(i *approval.GPGSignInfo) { i.Target = "lie" },
		"Pushee":     func(i *approval.GPGSignInfo) { i.Pushee = "lie" },
	}
	for field, lie := range lies {
		t.Run(field+" differs", func(t *testing.T) {
			info := bound()
			lie(&info)
			assert.True(t, p.BindDisplay(&info))
			assert.Equal(t, bound(), info, "parsed values replace the client's, RepoName and ChangedFiles stay")
		})
	}
}
