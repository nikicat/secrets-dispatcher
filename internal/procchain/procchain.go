// Package procchain traverses the Linux /proc filesystem to build a parent
// process chain starting from a given PID.
package procchain

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ProcInfo holds the process information extracted from /proc for a single PID.
type ProcInfo struct {
	PID  uint32
	PPid uint32
	Name string // from /proc/pid/comm (short command name, ≤15 chars)
	CWD  string // from readlink /proc/pid/cwd (empty if inaccessible)
}

// Walk traverses the process parent chain starting from startPID, returning up
// to maxDepth entries. The returned slice starts with startPID and each
// successive entry is the parent process. Traversal stops at PID 1, when
// maxDepth is reached, or when a process cannot be read (permission error,
// process exited, cycle detected).
//
// Partial results are returned on error — an empty slice means startPID is
// inaccessible or ≤ 1.
func Walk(startPID uint32, maxDepth int) []ProcInfo {
	var chain []ProcInfo
	current := startPID
	seen := make(map[uint32]bool)

	for i := 0; i < maxDepth; i++ {
		if current <= 1 || seen[current] {
			break
		}
		seen[current] = true

		info, err := readProc(current)
		if err != nil {
			break
		}
		chain = append(chain, info)
		current = info.PPid
	}
	return chain
}

// readProc reads process information for the given PID from /proc.
func readProc(pid uint32) (ProcInfo, error) {
	info := ProcInfo{PID: pid}

	// Read /proc/{pid}/status for PPid field.
	statusData, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return ProcInfo{}, fmt.Errorf("read /proc/%d/status: %w", pid, err)
	}
	ppid, err := parsePPid(statusData)
	if err != nil {
		return ProcInfo{}, fmt.Errorf("parse PPid from /proc/%d/status: %w", pid, err)
	}
	info.PPid = ppid

	// Read /proc/{pid}/comm for the short command name (preferred over
	// the Name field in status which may be truncated differently).
	commData, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err == nil {
		info.Name = strings.TrimRight(string(commData), "\n")
	}

	// Read /proc/{pid}/cwd symlink — may fail if the process is owned by
	// another user. Treat failure as empty CWD (non-fatal).
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err == nil {
		info.CWD = cwd
	}

	return info, nil
}

// parsePPid extracts the PPid value from /proc/{pid}/status content.
// The relevant line format is: "PPid:\t<number>\n"
func parsePPid(data []byte) (uint32, error) {
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "PPid:") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return 0, fmt.Errorf("malformed PPid line: %q", line)
		}
		n, err := strconv.ParseUint(parts[1], 10, 32)
		if err != nil {
			return 0, fmt.Errorf("parse PPid value %q: %w", parts[1], err)
		}
		return uint32(n), nil
	}
	return 0, fmt.Errorf("PPid field not found in /proc status")
}
