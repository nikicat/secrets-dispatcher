package approval

// smell: test fixtures in a non-test file ship in the binary. Kept: other
// packages' tests use them, and approval's tests cannot import a package that
// imports approval. Move them out once approval's own tests stop using them.

import (
	"cmp"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
)

// Returns a sender on the session bus whose invoker has the given command name
// and executable. Request.AutoApprovable accepts it.
func LocalSenderForTest(comm string, exe procutil.Path) SenderInfo {
	const pid = 4242
	return SenderInfo{
		Client:       ClientLocal,
		PID:          pid,
		InvokerName:  comm,
		ProcessChain: []procutil.Process{{Name: comm, PID: pid, Exe: &exe}},
	}
}

// Returns a sender on a remote client's bus, whose invoker has the given
// command name and which has no process chain (ADR 0003).
// Request.AutoApprovable rejects it.
func RemoteSenderForTest(comm string) SenderInfo {
	return SenderInfo{Client: "laptop", InvokerName: comm}
}

// Returns the sign info of the given object with the given staged files in the
// given repo, under key ABCD1234.
func SignInfoForTest(repo RepoName, object SignedObject, staged ...string) *GPGSignInfo {
	info, err := NewGPGSignInfo(object, repo, "ABCD1234", ListedFiles(staged))
	if err != nil {
		panic(err)
	}
	return info
}

// Returns the sign info, as SignInfoForTest makes it, of a commit with the
// given message.
func CommitSignInfoForTest(repo RepoName, message string, staged ...string) *GPGSignInfo {
	object := SignedObject("tree 0\nauthor A <a@x> 1 +0000\ncommitter A <a@x> 1 +0000\n\n" + message + "\n")
	return SignInfoForTest(repo, object, staged...)
}

// Returns a gpg_sign request from the given sender to sign a commit in the
// given repo, with the given files staged.
func CommitSignRequestForTest(sender SenderInfo, repo RepoName, staged ...string) RequestInfo {
	return CommitSignInfoForTest(repo, "fix", staged...).RequestInfo(sender)
}

// Returns a manager made from the given config. Unless the config sets its
// own, requests wait 5 minutes, so none expires mid-test, and History keeps
// 100 entries.
func NewManagerForTest(cfg ManagerConfig) *Manager {
	cfg.Timeout = cmp.Or(cfg.Timeout, 5*time.Minute)
	cfg.HistoryMax = cmp.Or(cfg.HistoryMax, 100)
	return NewManager(cfg)
}
