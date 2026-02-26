// Package procutil provides helpers for walking the Linux process tree
// via /proc to resolve the user-facing invoker of a request.
package procutil

import (
	"fmt"
	"os"
	"strings"
)

// shells is the set of known shell process names to skip when walking
// up the process tree to find the user-facing invoker.
var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true,
	"dash": true, "csh": true, "tcsh": true, "ksh": true,
}

// IsShell reports whether the given comm name is a known shell.
func IsShell(comm string) bool {
	return shells[comm]
}

// ReadComm reads the process name from /proc/<pid>/comm.
// Returns empty string on error.
func ReadComm(pid int32) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ReadPPID reads the parent PID from /proc/<pid>/stat.
// Returns 0 on any error.
func ReadPPID(pid int32) int32 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	// Format: "pid (comm) state ppid ..."
	// comm can contain spaces and parentheses, so find the last ')' first.
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return 0
	}
	// After ") " we have "state ppid ..."
	fields := strings.Fields(s[i+2:])
	if len(fields) < 2 {
		return 0
	}
	var ppid int32
	fmt.Sscanf(fields[1], "%d", &ppid)
	return ppid
}

// ResolveInvoker walks from pid up to init, skipping shell processes,
// to find the user-facing invoker. Returns the invoker's comm name and PID.
// Returns ("", 0) if /proc is unreadable.
func ResolveInvoker(pid uint32) (comm string, invokerPID uint32) {
	p := int32(pid)
	comm = ReadComm(p)
	if comm == "" {
		return "", 0
	}

	// If the direct process is already not a shell, return it.
	if !IsShell(comm) {
		return comm, pid
	}

	// Walk up the tree, skipping shells.
	for p = ReadPPID(p); p > 1; p = ReadPPID(p) {
		c := ReadComm(p)
		if c == "" {
			break
		}
		if !IsShell(c) {
			return c, uint32(p)
		}
	}

	// All ancestors are shells; return the original pid.
	return ReadComm(int32(pid)), pid
}
