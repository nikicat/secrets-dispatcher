package approval

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Wire JSON of GPGSignInfo.
var gpgSignInfoGolden = []struct {
	name string
	info GPGSignInfo
	wire string
}{
	{
		name: "commit",
		info: GPGSignInfo{
			RepoName: "repo",
			KeyID:    "KEY",
			Signed: SignedPayload{
				Message: "fix: thing",
				Signer:  "Alice <a@x>",
				Details: CommitDetails{Committer: "Bob <b@x>", ParentHash: "abc", ChangedFiles: []string{"a.go"}},
			},
			SignedObject: SignedObject("tree 0\nparent abc\nauthor Alice <a@x>\ncommitter Bob <b@x>\n\nfix: thing\n"),
		},
		wire: `{"repo_name":"repo","kind":"commit","commit_msg":"fix: thing","author":"Alice <a@x>","committer":"Bob <b@x>","key_id":"KEY","changed_files":["a.go"],"parent_hash":"abc","commit_object":"tree 0\nparent abc\nauthor Alice <a@x>\ncommitter Bob <b@x>\n\nfix: thing\n"}`,
	},
	{
		name: "tag",
		info: GPGSignInfo{
			RepoName: "repo",
			KeyID:    "KEY",
			Signed: SignedPayload{
				Message: "v1",
				Signer:  "Alice <a@x>",
				Details: TagDetails{Name: "v1.0", Target: "def"},
			},
			SignedObject: SignedObject("object def\ntype commit\ntag v1.0\ntagger Alice <a@x>\n\nv1\n"),
		},
		wire: `{"repo_name":"repo","kind":"tag","commit_msg":"v1","author":"Alice <a@x>","committer":"","key_id":"KEY","changed_files":null,"tag_name":"v1.0","target":"def","commit_object":"object def\ntype commit\ntag v1.0\ntagger Alice <a@x>\n\nv1\n"}`,
	},
	{
		name: "push",
		info: GPGSignInfo{
			RepoName: "repo",
			KeyID:    "KEY",
			Signed: SignedPayload{
				Message: "0 1 refs/heads/main",
				Signer:  "Alice <a@x>",
				Details: PushDetails{Pushee: "https://x/r.git"},
			},
			SignedObject: SignedObject("certificate version 0.1\npusher Alice <a@x>\npushee https://x/r.git\n\n0 1 refs/heads/main\n"),
		},
		wire: `{"repo_name":"repo","kind":"push","commit_msg":"0 1 refs/heads/main","author":"Alice <a@x>","committer":"","key_id":"KEY","changed_files":null,"pushee":"https://x/r.git","commit_object":"certificate version 0.1\npusher Alice <a@x>\npushee https://x/r.git\n\n0 1 refs/heads/main\n"}`,
	},
}

func TestGPGSignInfo_MarshalJSON(t *testing.T) {
	for _, tt := range gpgSignInfoGolden {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.info)
			require.NoError(t, err)
			assert.JSONEq(t, tt.wire, string(got))
		})
	}
}

func TestGPGSignInfo_UnmarshalJSON(t *testing.T) {
	for _, tt := range gpgSignInfoGolden {
		t.Run(tt.name, func(t *testing.T) {
			var got GPGSignInfo
			require.NoError(t, json.Unmarshal([]byte(tt.wire), &got))
			assert.Equal(t, tt.info, got)
		})
	}
}

func TestGPGSignInfo_UnmarshalJSON_IgnoresClientDisplayFields(t *testing.T) {
	var got GPGSignInfo
	require.NoError(t, json.Unmarshal([]byte(`{"kind":"push","commit_msg":"lie","author":"Mallory","tag_name":"v9","commit_object":"object def\ntag v1\n\nv1\n"}`), &got))
	assert.Equal(t, SignedPayload{Message: "v1", Details: TagDetails{Name: "v1", Target: "def"}}, got.Signed)
}

// Bytes that are not UTF-8, such as a Latin-1 commit message, are refused
// instead of reaching the daemon with U+FFFD in their place.
func TestGPGSignInfo_MarshalJSON_RefusesInvalidUTF8(t *testing.T) {
	info, err := NewGPGSignInfo(SignedObject("tree 0\n\ncaf\xe9\n"), "repo", "KEY", ListedFiles(nil))
	require.NoError(t, err)
	_, err = json.Marshal(info)
	assert.ErrorContains(t, err, "not valid UTF-8")
}

func TestGPGSignInfo_UnmarshalJSON_Refuses(t *testing.T) {
	for name, wire := range map[string]string{
		"no commit_object": `{"kind":"commit","commit_msg":"fix"}`,
		"oversized line":   `{"commit_object":"tree 0\n\n` + strings.Repeat("a", maxSignedLine+1) + `"}`,
		"not a git object": `{"commit_object":"\njunk"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var got GPGSignInfo
			assert.Error(t, json.Unmarshal([]byte(wire), &got))
		})
	}
}

func TestGPGSignInfo_MarshalJSON_RefusesMissingKind(t *testing.T) {
	_, err := json.Marshal(GPGSignInfo{SignedObject: SignedObject("tree 0\n\nfix\n")})
	assert.ErrorContains(t, err, "kind is not set")
}
