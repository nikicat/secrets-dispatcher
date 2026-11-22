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
	comm := self.Comm()
	if comm == "" {
		t.Fatal("Comm on self returned empty string")
	}
	t.Logf("self comm = %q", comm)
}

func TestComm_InvalidPID(t *testing.T) {
	comm := absent.Comm()
	if comm != "" {
		t.Errorf("expected empty string for invalid PID, got %q", comm)
	}
}

func TestParent_Self(t *testing.T) {
	ppid := self.Parent()
	if ppid == 0 {
		t.Fatal("Parent on self returned 0")
	}
	expected := PID(os.Getppid())
	if ppid != expected {
		t.Errorf("expected ppid %d, got %d", expected, ppid)
	}
}

func TestParent_InvalidPID(t *testing.T) {
	ppid := absent.Parent()
	if ppid != 0 {
		t.Errorf("expected 0 for invalid PID, got %d", ppid)
	}
}

func TestIsShell(t *testing.T) {
	for _, name := range []string{"sh", "bash", "zsh", "fish", "dash", "csh", "tcsh", "ksh"} {
		if !IsShell(name) {
			t.Errorf("expected %q to be a shell", name)
		}
	}
	for _, name := range []string{"chrome", "firefox", "git", "code", ""} {
		if IsShell(name) {
			t.Errorf("expected %q to NOT be a shell", name)
		}
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
	if len(chain) == 0 {
		t.Fatal("Chain(ChainToSessionStarter) returned empty chain")
	}

	// Session leader should be second-to-last (last is its parent).
	leaderParent := leaderPID.Parent()
	if leaderParent > 1 {
		if len(chain) < 2 {
			t.Fatal("expected at least 2 entries (session leader + parent)")
		}
		secondToLast := chain[len(chain)-2]
		if secondToLast.PID != leaderPID {
			t.Errorf("second-to-last PID = %d, want session leader %d", secondToLast.PID, leaderPID)
		}
		last := chain[len(chain)-1]
		if last.PID != leaderParent {
			t.Errorf("last PID = %d, want session leader parent %d", last.PID, leaderParent)
		}
	} else {
		// Session leader's parent is PID 1, so leader is last.
		last := chain[len(chain)-1]
		if last.PID != leaderPID {
			t.Errorf("last entry PID = %d, want session leader %d", last.PID, leaderPID)
		}
	}

	// Also verify: starting directly at the session leader should return it plus parent.
	leaderChain := leaderPID.Chain(ChainToSessionStarter)
	if len(leaderChain) == 0 {
		t.Fatal("Chain starting at session leader returned empty chain")
	}
	if leaderChain[0].PID != leaderPID {
		t.Errorf("chain[0].PID = %d, want %d", leaderChain[0].PID, leaderPID)
	}
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
	if len(chain) == 0 {
		t.Fatal("Chain returned empty chain")
	}
	entry := chain[0] // our own process
	if entry.Name == "" {
		t.Error("expected non-empty Name")
	}
	if _, ok := entry.ExePath(); !ok {
		t.Error("expected an Exe")
	}
	if _, ok := entry.CWDPath(); !ok {
		t.Error("expected a CWD")
	}
	if len(entry.Args) == 0 {
		t.Error("expected non-empty Args")
	}
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
