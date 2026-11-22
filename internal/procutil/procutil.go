// Package procutil provides helpers for walking the Linux process tree
// via /proc to resolve the user-facing invoker of a request.
package procutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// The process names that the walk to the user-facing invoker skips.
var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true,
	"dash": true, "csh": true, "tcsh": true, "ksh": true,
}

// Reports whether comm names a known shell.
func IsShell(comm string) bool {
	return shells[comm]
}

// The id of a process on this machine. Zero names no process.
type PID uint32

func (p PID) String() string { return strconv.FormatUint(uint64(p), 10) }

// Returns the path of the named file in p's /proc directory.
func (p PID) proc(name string) string { return "/proc/" + p.String() + "/" + name }

// Sends sig to p. Errors when p is gone or not the caller's to signal.
func (p PID) Signal(sig syscall.Signal) error { return syscall.Kill(int(p), sig) }

// Returns the process name from /proc/<pid>/comm, or "" when it is unreadable.
func (p PID) Comm() string {
	data, err := os.ReadFile(p.proc("comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Returns the fields of /proc/<pid>/stat after the comm: state, ppid, pgrp,
// session and so on. Nil when the file is unreadable.
func (p PID) statFields() []string {
	data, err := os.ReadFile(p.proc("stat"))
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

// Returns p's parent, or 0 when /proc/<pid>/stat is unreadable.
func (p PID) Parent() PID {
	fields := p.statFields()
	if len(fields) < 2 {
		return 0
	}
	var ppid PID
	fmt.Sscanf(fields[1], "%d", &ppid)
	return ppid
}

// Reports whether p leads its session (its session id is p).
func (p PID) IsSessionLeader() bool {
	fields := p.statFields()
	if len(fields) < 4 {
		return false
	}
	var sid PID
	fmt.Sscanf(fields[3], "%d", &sid)
	return sid == p
}

// An absolute file path, such as a process's executable or working directory.
type Path string

// Parses a path. Errors when text is not absolute.
func (p *Path) UnmarshalText(text []byte) error {
	if !filepath.IsAbs(string(text)) {
		return fmt.Errorf("path must be absolute: %q", text)
	}
	*p = Path(text)
	return nil
}

// One process of a chain, as /proc describes it.
type Process struct {
	// The process name, from /proc/<pid>/comm.
	Name string   `json:"name"`
	PID  PID      `json:"pid"`
	Exe  *Path    `json:"exe,omitempty"`
	Args []string `json:"args,omitempty"`
	CWD  *Path    `json:"cwd,omitempty"`
}

// Returns the process's executable, as PID.Exe reads it.
func (p Process) ExePath() (Path, bool) {
	if p.Exe == nil {
		return "", false
	}
	return *p.Exe, true
}

// Returns the process's working directory, as PID.CWD reads it.
func (p Process) CWDPath() (Path, bool) {
	if p.CWD == nil {
		return "", false
	}
	return *p.CWD, true
}

func (p PID) entry(comm string) Process {
	proc := Process{Name: comm, PID: p, Args: p.Cmdline()}
	if exe, ok := p.Exe(); ok {
		proc.Exe = &exe
	}
	if cwd, ok := p.CWD(); ok {
		proc.CWD = &cwd
	}
	return proc
}

// Returns the path of p's executable. The path stays as it was when the binary
// is replaced on disk, as after a package upgrade, without Linux's " (deleted)"
// suffix. Reports false when /proc/<pid>/exe is unreadable.
func (p PID) Exe() (Path, bool) {
	target, err := os.Readlink(p.proc("exe"))
	if err != nil {
		return "", false
	}
	return Path(strings.TrimSuffix(target, " (deleted)")), true
}

// Returns p's working directory. Reports false when /proc/<pid>/cwd is
// unreadable.
func (p PID) CWD() (Path, bool) {
	target, err := os.Readlink(p.proc("cwd"))
	if err != nil {
		return "", false
	}
	return Path(target), true
}

// Where a process chain ends.
type ChainEnd int

const (
	// Runs the chain up to, but not including, PID 1.
	ChainToInit ChainEnd = iota
	// Ends the chain at the parent of the first session leader, which shows
	// what started the session, such as an IDE.
	ChainToSessionStarter
)

// Returns p and its ancestors, nearest first, ending as end says.
func (p PID) Chain(end ChainEnd) []Process {
	var chain []Process
	for q := p; q > 1; q = q.Parent() {
		comm := q.Comm()
		if comm == "" {
			break
		}
		chain = append(chain, q.entry(comm))
		if end == ChainToSessionStarter && q.IsSessionLeader() {
			parent := q.Parent()
			if parent > 1 {
				if pcomm := parent.Comm(); pcomm != "" {
					chain = append(chain, parent.entry(pcomm))
				}
			}
			break
		}
	}
	return chain
}

// Returns p's argv from /proc/<pid>/cmdline, or nil when it is unreadable or
// empty.
func (p PID) Cmdline() []string {
	data, err := os.ReadFile(p.proc("cmdline"))
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

// Returns p's child processes from /proc/<pid>/task/<pid>/children (Linux
// 3.5+), or nil when it is unreadable.
func (p PID) Children() []PID {
	data, err := os.ReadFile(p.proc("task/" + p.String() + "/children"))
	if err != nil || len(data) == 0 {
		return nil
	}
	fields := strings.Fields(string(data))
	children := make([]PID, 0, len(fields))
	for _, f := range fields {
		var child PID
		if _, err := fmt.Sscanf(f, "%d", &child); err == nil {
			children = append(children, child)
		}
	}
	return children
}

// Returns the host that p, or an ssh child of it such as git's, connects to.
// Reports false when neither is an ssh with a host on its command line.
func (p PID) SSHDestination() (string, bool) {
	// Check if the process itself is ssh
	comm := p.Comm()
	if comm == "ssh" || comm == "ssh.exe" {
		if host, ok := parseSSHHost(p.Cmdline()); ok {
			return host, true
		}
	}

	// Walk children looking for ssh (covers git → ssh, scp → ssh, etc.)
	for _, child := range p.Children() {
		childComm := child.Comm()
		if childComm == "ssh" || childComm == "ssh.exe" {
			if host, ok := parseSSHHost(child.Cmdline()); ok {
				return host, true
			}
		}
	}

	return "", false
}

// Returns the host of an ssh command line, "ssh [options] [user@]hostname
// [command...]". Reports false when it names none.
func parseSSHHost(argv []string) (string, bool) {
	if len(argv) < 2 {
		return "", false
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
				return stripUser(argv[i+1]), true
			}
			return "", false
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
		return stripUser(arg), true
	}
	return "", false
}

// Returns s without its "user@" prefix.
func stripUser(s string) string {
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// The user-facing process behind a request. It is the nearest process, from
// the caller up, that is not a shell.
type Invoker struct {
	// The process name, from /proc/<pid>/comm.
	Name string
	PID  PID
}

// Returns the invoker of p, which is p itself when p and all its ancestors are
// shells. Reports false when /proc has no p.
func (p PID) Invoker() (Invoker, bool) {
	comm := p.Comm()
	if comm == "" {
		return Invoker{}, false
	}

	// If the direct process is already not a shell, return it.
	if !IsShell(comm) {
		return Invoker{Name: comm, PID: p}, true
	}

	// Walk up the tree, skipping shells.
	for q := p.Parent(); q > 1; q = q.Parent() {
		c := q.Comm()
		if c == "" {
			break
		}
		if !IsShell(c) {
			return Invoker{Name: c, PID: q}, true
		}
	}

	// All ancestors are shells; return the original pid.
	return Invoker{Name: comm, PID: p}, true
}
