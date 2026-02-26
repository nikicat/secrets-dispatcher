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

func TestResolveInvoker_InvalidPID(t *testing.T) {
	comm, pid := ResolveInvoker(0)
	if comm != "" {
		t.Errorf("expected empty comm for invalid PID, got %q", comm)
	}
	if pid != 0 {
		t.Errorf("expected pid 0 for invalid PID, got %d", pid)
	}
}
