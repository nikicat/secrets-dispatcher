package procutil

import (
	"os"
	"testing"
)

func TestReadComm_Self(t *testing.T) {
	comm := ReadComm(int32(os.Getpid()))
	if comm == "" {
		t.Fatal("ReadComm on self returned empty string")
	}
	t.Logf("self comm = %q", comm)
}

func TestReadComm_InvalidPID(t *testing.T) {
	comm := ReadComm(-1)
	if comm != "" {
		t.Errorf("expected empty string for invalid PID, got %q", comm)
	}
}

func TestReadPPID_Self(t *testing.T) {
	ppid := ReadPPID(int32(os.Getpid()))
	if ppid == 0 {
		t.Fatal("ReadPPID on self returned 0")
	}
	expected := int32(os.Getppid())
	if ppid != expected {
		t.Errorf("expected ppid %d, got %d", expected, ppid)
	}
}

func TestReadPPID_InvalidPID(t *testing.T) {
	ppid := ReadPPID(-1)
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

func TestResolveInvoker_Self(t *testing.T) {
	comm, pid := ResolveInvoker(uint32(os.Getpid()))
	if comm == "" {
		t.Fatal("ResolveInvoker on self returned empty comm")
	}
	if pid == 0 {
		t.Fatal("ResolveInvoker on self returned pid 0")
	}
	t.Logf("invoker: %s [%d]", comm, pid)
}

func TestReadProcessChain_TrimIncludesSessionLeader(t *testing.T) {
	pid := int32(os.Getpid())

	// Find the nearest session leader ancestor.
	var leaderPID int32
	for p := pid; p > 1; p = ReadPPID(p) {
		if IsSessionLeader(p) {
			leaderPID = p
			break
		}
	}
	if leaderPID == 0 {
		t.Skip("no session leader ancestor found")
	}

	// With trim=true, chain should include processes up to the leader
	// plus one more entry (the parent of the session leader).
	chain := ReadProcessChain(pid, true)
	if len(chain) == 0 {
		t.Fatal("ReadProcessChain(trim=true) returned empty chain")
	}

	// Session leader should be second-to-last (last is its parent).
	leaderParent := ReadPPID(leaderPID)
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
	leaderChain := ReadProcessChain(leaderPID, true)
	if len(leaderChain) == 0 {
		t.Fatal("ReadProcessChain starting at session leader returned empty chain")
	}
	if leaderChain[0].PID != leaderPID {
		t.Errorf("chain[0].PID = %d, want %d", leaderChain[0].PID, leaderPID)
	}
}

func TestReadExe_Self(t *testing.T) {
	exe := ReadExe(int32(os.Getpid()))
	if exe == "" {
		t.Fatal("ReadExe on self returned empty string")
	}
	t.Logf("self exe = %q", exe)
}

func TestReadExe_InvalidPID(t *testing.T) {
	exe := ReadExe(-1)
	if exe != "" {
		t.Errorf("expected empty string for invalid PID, got %q", exe)
	}
}

func TestReadCWD_Self(t *testing.T) {
	cwd := ReadCWD(int32(os.Getpid()))
	if cwd == "" {
		t.Fatal("ReadCWD on self returned empty string")
	}
	expected, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if cwd != expected {
		t.Errorf("expected cwd %q, got %q", expected, cwd)
	}
}

func TestReadCWD_InvalidPID(t *testing.T) {
	cwd := ReadCWD(-1)
	if cwd != "" {
		t.Errorf("expected empty string for invalid PID, got %q", cwd)
	}
}

func TestReadProcessChain_PopulatesAllFields(t *testing.T) {
	chain := ReadProcessChain(int32(os.Getpid()), false)
	if len(chain) == 0 {
		t.Fatal("ReadProcessChain returned empty chain")
	}
	entry := chain[0] // our own process
	if entry.Comm == "" {
		t.Error("expected non-empty Comm")
	}
	if entry.Exe == "" {
		t.Error("expected non-empty Exe")
	}
	if entry.CWD == "" {
		t.Error("expected non-empty CWD")
	}
	if len(entry.Args) == 0 {
		t.Error("expected non-empty Args")
	}
	t.Logf("self: comm=%q exe=%q cwd=%q args=%v", entry.Comm, entry.Exe, entry.CWD, entry.Args)
}

func TestResolveInvoker_InvalidPID(t *testing.T) {
	comm, pid := ResolveInvoker(0)
	if comm != "" {
		t.Errorf("expected empty comm for invalid PID, got %q", comm)
	}
	if pid != 0 {
		t.Errorf("expected pid 0 for invalid PID, got %d", pid)
	}
}
