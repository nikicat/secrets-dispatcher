// Package sender describes the process behind a request, in the form that the
// API sends and the CLI reads.
package sender

import (
	"fmt"
	"os"
	"slices"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// A D-Bus unique connection name, such as ":1.123".
type UniqueName string

// The process behind a request.
type Info struct {
	// Nil when the request did not come over D-Bus.
	Sender *UniqueName `json:"sender,omitempty"`
	// The PID of the invoker (procutil.Invoker).
	PID procutil.PID `json:"pid"`
	// Nil when the transport does not report the caller's user.
	UID *procutil.UID `json:"uid,omitempty"`
	// The invoker's process name, which any process can set for itself. Empty
	// when /proc has no chain for the caller.
	InvokerName string `json:"invoker_name"`
	// The caller's unit as systemd reports it. Unlike a process name, the
	// caller cannot choose it.
	SystemdUnit systemd.Unit `json:"systemd_unit,omitempty"`
	// The caller and its ancestors, nearest first, as ChainReader.Chain returns
	// them.
	ProcessChain []procutil.Process `json:"process_chain,omitempty"`
}

// Names the caller for display: the invoker's process name, or else its
// systemd unit.
func (i Info) DisplayName() string {
	if i.InvokerName != "" {
		return i.InvokerName
	}
	return string(i.SystemdUnit)
}

// Reads the process chains of callers on this machine. The zero value leaves
// no process out and keeps each chain whole.
type ChainReader struct {
	end     procutil.ChainEnd
	selfExe procutil.Path
}

// Returns a reader whose chains end as end says and leave out our own
// executable, which relays requests and makes none. Errors when the path of
// our own executable is unknown.
func NewChainReader(end procutil.ChainEnd) (ChainReader, error) {
	exe, err := os.Executable()
	if err != nil {
		return ChainReader{}, fmt.Errorf("find our own executable: %w", err)
	}
	return ChainReader{end: end, selfExe: procutil.Path(exe)}, nil
}

// Reports whether exe is our own executable.
func (r ChainReader) IsSelf(exe procutil.Path) bool {
	return exe == r.selfExe
}

// Returns pid and its ancestors, nearest first, cut as NewChainReader says.
func (r ChainReader) Chain(pid procutil.PID) []procutil.Process {
	chain := pid.Chain(r.end)
	return slices.DeleteFunc(chain, func(p procutil.Process) bool { return p.Exe != nil && r.IsSelf(*p.Exe) })
}
