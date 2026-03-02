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
	Exe  string   // readlink /proc/PID/exe
	Args []string // /proc/PID/cmdline
	CWD  string   // readlink /proc/PID/cwd
}

// ReadExe reads the executable path from /proc/<pid>/exe.
// Returns empty string on error (e.g., permission denied, process gone).
func ReadExe(pid int32) string {
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return target
}

// ReadCWD reads the current working directory from /proc/<pid>/cwd.
// Returns empty string on error.
func ReadCWD(pid int32) string {
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	return target
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
		chain = append(chain, ProcEntry{
			Comm: comm,
			PID:  p,
			Exe:  ReadExe(p),
			Args: ReadCmdline(p),
			CWD:  ReadCWD(p),
		})
		if trimAtSessionLeader && IsSessionLeader(p) {
			break
		}
	}
	return chain
}

// ReadCmdline reads /proc/<pid>/cmdline and returns the argv slice.
// Returns nil on error.
func ReadCmdline(pid int32) []string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(data) == 0 {
		return nil
	}
	// cmdline is NUL-separated; trim trailing NUL
	s := strings.TrimRight(string(data), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// ReadChildren returns PIDs of child processes of the given PID.
// Reads /proc/<pid>/task/<pid>/children (Linux 3.5+).
func ReadChildren(pid int32) []int32 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil || len(data) == 0 {
		return nil
	}
	fields := strings.Fields(string(data))
	children := make([]int32, 0, len(fields))
	for _, f := range fields {
		var child int32
		if _, err := fmt.Sscanf(f, "%d", &child); err == nil {
			children = append(children, child)
		}
	}
	return children
}

// ResolveSSHDestination tries to extract the SSH destination host from
// the process identified by pid. It checks:
//  1. If pid itself is an ssh process, parse its cmdline for the hostname.
//  2. Otherwise, look for an ssh child process (covers git → ssh).
//
// Returns empty string if no destination can be determined.
func ResolveSSHDestination(pid int32) string {
	// Check if the process itself is ssh
	comm := ReadComm(pid)
	if comm == "ssh" || comm == "ssh.exe" {
		if host := parseSSHHost(ReadCmdline(pid)); host != "" {
			return host
		}
	}

	// Walk children looking for ssh (covers git → ssh, scp → ssh, etc.)
	for _, child := range ReadChildren(pid) {
		childComm := ReadComm(child)
		if childComm == "ssh" || childComm == "ssh.exe" {
			if host := parseSSHHost(ReadCmdline(child)); host != "" {
				return host
			}
		}
	}

	return ""
}

// parseSSHHost extracts the destination from an ssh command line.
// Handles "ssh [options] [user@]hostname [command...]".
// Returns empty string if parsing fails.
func parseSSHHost(argv []string) string {
	if len(argv) < 2 {
		return ""
	}

	// Skip argv[0] (the ssh binary path)
	// Walk arguments, skipping flags that take a value
	flagsWithArg := map[string]bool{
		"-b": true, "-c": true, "-D": true, "-E": true, "-e": true,
		"-F": true, "-I": true, "-i": true, "-J": true, "-L": true,
		"-l": true, "-m": true, "-O": true, "-o": true, "-p": true,
		"-Q": true, "-R": true, "-S": true, "-W": true, "-w": true,
	}

	i := 1
	for i < len(argv) {
		arg := argv[i]
		if arg == "--" {
			// Next arg after -- is the hostname
			if i+1 < len(argv) {
				return stripUser(argv[i+1])
			}
			return ""
		}
		if len(arg) > 1 && arg[0] == '-' {
			if flagsWithArg[arg] {
				i += 2 // skip flag and its argument
				continue
			}
			// Flags without arguments (might be combined like -vvv)
			i++
			continue
		}
		// First non-flag argument is the hostname
		return stripUser(arg)
	}
	return ""
}

// stripUser removes the "user@" prefix from a hostname string.
func stripUser(s string) string {
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		return s[i+1:]
	}
	return s
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
