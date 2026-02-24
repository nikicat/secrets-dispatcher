package gpgsign

import (
	"testing"
)

func TestParseCommitObject(t *testing.T) {
	t.Run("standard commit without parent", func(t *testing.T) {
		data := []byte(
			"tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
				"author Alice Example <alice@example.com> 1771936651 +0200\n" +
				"committer Alice Example <alice@example.com> 1771936651 +0200\n" +
				"\n" +
				"feat: add files\n",
		)
		author, committer, message, parentHash := ParseCommitObject(data)

		if author != "Alice Example <alice@example.com> 1771936651 +0200" {
			t.Errorf("author = %q, want %q", author, "Alice Example <alice@example.com> 1771936651 +0200")
		}
		if committer != "Alice Example <alice@example.com> 1771936651 +0200" {
			t.Errorf("committer = %q, want %q", committer, "Alice Example <alice@example.com> 1771936651 +0200")
		}
		if message != "feat: add files" {
			t.Errorf("message = %q, want %q", message, "feat: add files")
		}
		if parentHash != "" {
			t.Errorf("parentHash = %q, want empty", parentHash)
		}
	})

	t.Run("commit with single parent", func(t *testing.T) {
		data := []byte(
			"tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
				"parent abc123def456\n" +
				"author Alice Example <alice@example.com> 1771936651 +0200\n" +
				"committer Alice Example <alice@example.com> 1771936651 +0200\n" +
				"\n" +
				"fix: correct logic\n",
		)
		_, _, _, parentHash := ParseCommitObject(data)

		if parentHash != "abc123def456" {
			t.Errorf("parentHash = %q, want %q", parentHash, "abc123def456")
		}
	})

	t.Run("merge commit with multiple parents uses first", func(t *testing.T) {
		data := []byte(
			"tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
				"parent aaa111bbb222\n" +
				"parent ccc333ddd444\n" +
				"author Alice Example <alice@example.com> 1771936651 +0200\n" +
				"committer Alice Example <alice@example.com> 1771936651 +0200\n" +
				"\n" +
				"Merge branch 'feature'\n",
		)
		_, _, _, parentHash := ParseCommitObject(data)

		if parentHash != "aaa111bbb222" {
			t.Errorf("parentHash = %q, want %q (first parent)", parentHash, "aaa111bbb222")
		}
	})

	t.Run("empty message", func(t *testing.T) {
		data := []byte(
			"tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
				"author Alice Example <alice@example.com> 1771936651 +0200\n" +
				"committer Alice Example <alice@example.com> 1771936651 +0200\n" +
				"\n",
		)
		_, _, message, _ := ParseCommitObject(data)

		if message != "" {
			t.Errorf("message = %q, want empty string", message)
		}
	})

	t.Run("multi-line message preserved", func(t *testing.T) {
		data := []byte(
			"tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
				"author Alice Example <alice@example.com> 1771936651 +0200\n" +
				"committer Alice Example <alice@example.com> 1771936651 +0200\n" +
				"\n" +
				"feat: big feature\n" +
				"\n" +
				"This is a longer description.\n" +
				"Spanning multiple lines.\n",
		)
		_, _, message, _ := ParseCommitObject(data)

		want := "feat: big feature\n\nThis is a longer description.\nSpanning multiple lines."
		if message != want {
			t.Errorf("message = %q, want %q", message, want)
		}
	})

	t.Run("trailing newline stripped", func(t *testing.T) {
		data := []byte(
			"tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\n" +
				"author Alice Example <alice@example.com> 1771936651 +0200\n" +
				"committer Alice Example <alice@example.com> 1771936651 +0200\n" +
				"\n" +
				"chore: update deps\n",
		)
		_, _, message, _ := ParseCommitObject(data)

		if message != "chore: update deps" {
			t.Errorf("message = %q, want %q", message, "chore: update deps")
		}
	})
}
