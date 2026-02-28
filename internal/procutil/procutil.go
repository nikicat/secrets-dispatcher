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

// readStatFields parses /proc/<pid>/stat and returns the fields after ") ".
// Format: "pid (comm) state ppid pgrp session ..."
// Returns nil on error.
func readStatFields(pid int32) []string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil
	}
	s := string(data)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return nil
	}
	return strings.Fields(s[i+2:])
}

// ReadPPID reads the parent PID from /proc/<pid>/stat.
// Returns 0 on any error.
func ReadPPID(pid int32) int32 {
	fields := readStatFields(pid)
	if len(fields) < 2 {
		return 0
	}
	var ppid int32
	fmt.Sscanf(fields[1], "%d", &ppid)
	return ppid
}

// IsSessionLeader reports whether pid is a session leader (SID == PID).
func IsSessionLeader(pid int32) bool {
	fields := readStatFields(pid)
	if len(fields) < 4 {
		return false
	}
	// fields[0]=state, [1]=ppid, [2]=pgrp, [3]=session
	var sid int32
	fmt.Sscanf(fields[3], "%d", &sid)
	return sid == pid
}

// ProcEntry represents a single process in the process chain.
type ProcEntry struct {
	Comm string
	PID  int32
}

// ReadProcessChain walks from pid up to (but not including) PID 1,
// returning the process chain. When trimAtSessionLeader is true, the
// walk stops after including the first session leader encountered
// (the process whose SID equals its PID).
func ReadProcessChain(pid int32, trimAtSessionLeader bool) []ProcEntry {
	var chain []ProcEntry
	for p := pid; p > 1; p = ReadPPID(p) {
		comm := ReadComm(p)
		if comm == "" {
			break
		}
		if trimAtSessionLeader && IsSessionLeader(p) {
			break
		}
		chain = append(chain, ProcEntry{Comm: comm, PID: p})
	}
	return chain
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
