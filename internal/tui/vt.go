// Package tui provides VT (Virtual Terminal) ioctl helpers and related types
// for the secrets-dispatcher companion daemon's interactive approval TUI.
//
// VT ioctl constants are from /usr/include/linux/vt.h (verified on Linux).
// All ioctl calls use syscall.Syscall with SYS_IOCTL for consistency with the
// existing codebase which uses the syscall package rather than golang.org/x/sys/unix.
package tui

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// LockMode controls when VT_PROCESS mode (kernel-enforced VT switching lock)
// is engaged. See 05-RESEARCH.md for rationale.
type LockMode int

const (
	// LockModeNone: y/n approvals work at any time; VT_PROCESS is never engaged.
	// This is the safe default that avoids the VT_SETMODE race with display managers.
	LockModeNone LockMode = iota

	// LockModeManual: user explicitly locks/unlocks the VT via keyboard shortcut.
	// VT_PROCESS is engaged only while locked.
	LockModeManual

	// LockModeAuto: VT locks automatically when a pending request is selected,
	// unlocks when the cursor moves away or the request is resolved.
	LockModeAuto
)

// VT ioctl constants from /usr/include/linux/vt.h.
// Verified on Linux kernel 6.18.8-zen2-1-zen.
const (
	vtSetMode     = 0x5602 // VT_SETMODE: set mode of active VT
	vtRelDisp     = 0x5605 // VT_RELDISP: allow/disallow VT switch
	vtModeAuto    = 0x00   // VT_AUTO: allow kernel to switch VTs freely
	vtModeProcess = 0x01   // VT_PROCESS: process controls VT switching
)

// vtMode mirrors struct vt_mode from /usr/include/linux/vt.h.
// Total size must be 8 bytes to match the kernel ABI.
//
//	struct vt_mode {
//	    char   mode;    // VT_AUTO or VT_PROCESS
//	    char   waitv;   // if set, hang process on write if not active
//	    short  relsig;  // signal to raise on release request
//	    short  acqsig;  // signal to raise on acquisition
//	    short  frsig;   // unused (must be 0)
//	};
type vtMode struct {
	mode   uint8
	waitv  uint8
	relsig int16
	acqsig int16
	frsig  int16
}

// OpenVT opens the VT device at path (e.g. "/dev/tty8") with O_RDWR.
// The returned *os.File can be passed to bubbletea via WithInput/WithOutput.
// Caller is responsible for closing the file.
func OpenVT(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open VT %q: %w", path, err)
	}
	return f, nil
}

// LockVT engages VT_PROCESS mode on the given fd. While locked, the kernel
// will send SIGUSR1 to the owning process before allowing any VT switch. The
// process must respond with AckRelease to grant the switch; failing to do so
// will cause VT switching to hang indefinitely.
//
// The caller must register a SIGUSR1 handler (via CleanupOnSignal or manually)
// before calling LockVT to avoid stalled VT switches.
func LockVT(fd uintptr) error {
	m := vtMode{
		mode:   vtModeProcess,
		relsig: int16(syscall.SIGUSR1), // kernel sends SIGUSR1 to request release
		acqsig: int16(syscall.SIGUSR2), // kernel sends SIGUSR2 on acquisition
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, vtSetMode, uintptr(unsafe.Pointer(&m)))
	if errno != 0 {
		return fmt.Errorf("VT_SETMODE(VT_PROCESS): %w", errno)
	}
	return nil
}

// UnlockVT restores VT_AUTO mode on the given fd, allowing the kernel to
// switch VTs freely without process involvement.
func UnlockVT(fd uintptr) error {
	m := vtMode{mode: vtModeAuto}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, vtSetMode, uintptr(unsafe.Pointer(&m)))
	if errno != 0 {
		return fmt.Errorf("VT_SETMODE(VT_AUTO): %w", errno)
	}
	return nil
}

// AckRelease acknowledges a VT release request from the kernel. This must be
// called from the SIGUSR1 handler after LockVT has been called. If not called,
// the VT switch will hang until the process dies.
//
// Value 1 means "release allowed". Value 0 means "release denied" (not used here).
func AckRelease(fd uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, vtRelDisp, 1)
	if errno != 0 {
		return fmt.Errorf("VT_RELDISP: %w", errno)
	}
	return nil
}

// CleanupOnSignal registers signal handlers for SIGTERM and SIGINT that call
// UnlockVT before the process exits, and a SIGUSR1 goroutine that calls
// AckRelease to grant VT switch requests from the kernel.
//
// Returns a cleanup function that should be called via defer to unregister the
// handlers and restore VT_AUTO on normal shutdown.
//
// Note: SIGKILL cannot be caught, but the Linux kernel automatically releases
// VT_PROCESS mode when the owning process dies — no recovery handler is needed
// for kill -9. The returned cleanup function handles clean (non-kill-9) shutdown.
func CleanupOnSignal(fd uintptr) func() {
	sigCh := make(chan os.Signal, 1)
	usr1Ch := make(chan os.Signal, 4) // buffer to avoid dropping signals

	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	signal.Notify(usr1Ch, syscall.SIGUSR1)

	// SIGUSR1 goroutine: acknowledge VT release requests from the kernel.
	// Must respond promptly or VT switching stalls for all users.
	go func() {
		for range usr1Ch {
			if err := AckRelease(fd); err != nil {
				slog.Warn("VT_RELDISP failed", "err", err)
			}
		}
	}()

	// SIGTERM/SIGINT goroutine: restore VT_AUTO on clean shutdown.
	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}
		slog.Info("received signal, restoring VT_AUTO", "signal", sig)
		if err := UnlockVT(fd); err != nil {
			slog.Warn("UnlockVT on signal failed", "err", err)
		}
		// Re-raise the signal with default handler so the process exits normally.
		signal.Reset(sig.(syscall.Signal))
		proc, _ := os.FindProcess(os.Getpid())
		if proc != nil {
			_ = proc.Signal(sig)
		}
	}()

	// Return a cleanup function for deferred use during normal (non-signal) shutdown.
	return func() {
		signal.Stop(sigCh)
		signal.Stop(usr1Ch)
		close(sigCh)
		close(usr1Ch)
		if err := UnlockVT(fd); err != nil {
			slog.Warn("UnlockVT on cleanup failed", "err", err)
		}
	}
}
