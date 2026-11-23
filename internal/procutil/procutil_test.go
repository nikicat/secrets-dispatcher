package procutil

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var self = PID(os.Getpid())

// Above Linux's PID_MAX_LIMIT, so no process has it.
const absent PID = 1 << 30

func TestComm_Self(t *testing.T) {
	assert.NotEmpty(t, self.Comm())
}

func TestComm_InvalidPID(t *testing.T) {
	assert.Empty(t, absent.Comm())
}

func TestParent_Self(t *testing.T) {
	assert.Equal(t, PID(os.Getppid()), self.Parent())
}

func TestParent_InvalidPID(t *testing.T) {
	assert.Zero(t, absent.Parent())
}

func TestIsShell(t *testing.T) {
	for _, name := range []string{"sh", "bash", "zsh", "fish", "dash", "csh", "tcsh", "ksh"} {
		assert.True(t, IsShell(name), name)
	}
	for _, name := range []string{"chrome", "firefox", "git", "code", ""} {
		assert.False(t, IsShell(name), name)
	}
}

func TestInvoker_Self(t *testing.T) {
	invoker, ok := self.Invoker()
	require.True(t, ok)
	assert.NotEmpty(t, invoker.Name)
	assert.NotZero(t, invoker.PID)
}

func TestChain_TrimIncludesSessionLeader(t *testing.T) {
	pid := self

	// Find the nearest session leader ancestor.
	var leaderPID PID
	for p := pid; p > 1; p = p.Parent() {
		if p.IsSessionLeader() {
			leaderPID = p
			break
		}
	}
	if leaderPID == 0 {
		t.Skip("no session leader ancestor found")
	}

	// With ChainToSessionStarter, the chain should include processes up to the leader
	// plus one more entry (the parent of the session leader).
	chain := pid.Chain(ChainToSessionStarter)
	require.NotEmpty(t, chain)

	// Session leader should be second-to-last (last is its parent).
	leaderParent := leaderPID.Parent()
	if leaderParent > 1 {
		require.GreaterOrEqual(t, len(chain), 2, "session leader and its parent")
		assert.Equal(t, leaderPID, chain[len(chain)-2].PID)
		assert.Equal(t, leaderParent, chain[len(chain)-1].PID)
	} else {
		// Session leader's parent is PID 1, so leader is last.
		assert.Equal(t, leaderPID, chain[len(chain)-1].PID)
	}

	// Also verify: starting directly at the session leader should return it plus parent.
	leaderChain := leaderPID.Chain(ChainToSessionStarter)
	require.NotEmpty(t, leaderChain)
	assert.Equal(t, leaderPID, leaderChain[0].PID)
}

func TestExe_Self(t *testing.T) {
	exe, ok := self.Exe()
	require.True(t, ok)
	assert.True(t, filepath.IsAbs(string(exe)), "exe %q", exe)
}

func TestExe_InvalidPID(t *testing.T) {
	_, ok := absent.Exe()
	assert.False(t, ok)
}

func TestCWD_Self(t *testing.T) {
	cwd, ok := self.CWD()
	require.True(t, ok)
	expected, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, Path(expected), cwd)
}

func TestCWD_InvalidPID(t *testing.T) {
	_, ok := absent.CWD()
	assert.False(t, ok)
}

func TestChain_PopulatesAllFields(t *testing.T) {
	chain := self.Chain(ChainToInit)
	require.NotEmpty(t, chain)
	entry := chain[0] // our own process
	assert.NotEmpty(t, entry.Name)
	_, ok := entry.ExePath()
	assert.True(t, ok, "an Exe")
	_, ok = entry.CWDPath()
	assert.True(t, ok, "a CWD")
	assert.NotEmpty(t, entry.Args)
}

func TestInvoker_InvalidPID(t *testing.T) {
	_, ok := absent.Invoker()
	assert.False(t, ok)
}

// A binary replaced on disk keeps its path, without Linux's " (deleted)".
func TestExe_Deleted(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "sleeper")
	data, err := os.ReadFile("/bin/sleep")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(exe, data, 0o755))
	cmd := exec.Command(exe, "60")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	require.NoError(t, os.Remove(exe))

	pid := PID(cmd.Process.Pid)
	require.Eventually(t, func() bool { return pid.Comm() == "sleeper" }, 5*time.Second, 10*time.Millisecond)
	got, ok := pid.Exe()
	require.True(t, ok)
	assert.Equal(t, Path(exe), got)
}

// A process whose /proc links are unreadable has no "exe" or "cwd" key.
func TestProcessJSONOmitsUnknownPaths(t *testing.T) {
	exe := Path("/usr/bin/git")
	known, err := json.Marshal(Process{Name: "git", PID: 7, Exe: &exe, CWD: &exe})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"git","pid":7,"exe":"/usr/bin/git","cwd":"/usr/bin/git"}`, string(known))
	unknown, err := json.Marshal(Process{Name: "git", PID: 7})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"git","pid":7}`, string(unknown))
}

func TestPathUnmarshalText(t *testing.T) {
	var p Path
	require.NoError(t, p.UnmarshalText([]byte("/usr/bin/git")))
	assert.Equal(t, Path("/usr/bin/git"), p)
	assert.EqualError(t, p.UnmarshalText([]byte("bin/git")), `path must be absolute: "bin/git"`)
	assert.Error(t, p.UnmarshalText(nil))
}

func TestSignal(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "60")
	require.NoError(t, cmd.Start())

	require.NoError(t, PID(cmd.Process.Pid).Signal(syscall.SIGTERM))
	err := cmd.Wait()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, syscall.SIGTERM, exitErr.Sys().(syscall.WaitStatus).Signal())
}
