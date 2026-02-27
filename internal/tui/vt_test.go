package tui

import (
	"os"
	"testing"
	"unsafe"
)

// TestVTModeStructSize verifies the vtMode struct has the correct 8-byte layout
// to match the kernel's struct vt_mode ABI from /usr/include/linux/vt.h.
//
//	struct vt_mode {
//	    char  mode;    // 1 byte
//	    char  waitv;   // 1 byte
//	    short relsig;  // 2 bytes
//	    short acqsig;  // 2 bytes
//	    short frsig;   // 2 bytes
//	};                 // total: 8 bytes
func TestVTModeStructSize(t *testing.T) {
	const wantSize = 8
	got := unsafe.Sizeof(vtMode{})
	if got != wantSize {
		t.Errorf("sizeof(vtMode) = %d, want %d; struct layout does not match kernel ABI", got, wantSize)
	}
}

// TestVTModeFieldOffsets verifies individual field offsets within vtMode match
// the kernel struct layout (important for ioctl binary compatibility).
func TestVTModeFieldOffsets(t *testing.T) {
	var m vtMode

	// mode is at offset 0 (char)
	modeOffset := unsafe.Offsetof(m.mode)
	if modeOffset != 0 {
		t.Errorf("vtMode.mode offset = %d, want 0", modeOffset)
	}

	// waitv is at offset 1 (char)
	waitvOffset := unsafe.Offsetof(m.waitv)
	if waitvOffset != 1 {
		t.Errorf("vtMode.waitv offset = %d, want 1", waitvOffset)
	}

	// relsig is at offset 2 (short)
	relsigOffset := unsafe.Offsetof(m.relsig)
	if relsigOffset != 2 {
		t.Errorf("vtMode.relsig offset = %d, want 2", relsigOffset)
	}

	// acqsig is at offset 4 (short)
	acqsigOffset := unsafe.Offsetof(m.acqsig)
	if acqsigOffset != 4 {
		t.Errorf("vtMode.acqsig offset = %d, want 4", acqsigOffset)
	}

	// frsig is at offset 6 (short)
	frsigOffset := unsafe.Offsetof(m.frsig)
	if frsigOffset != 6 {
		t.Errorf("vtMode.frsig offset = %d, want 6", frsigOffset)
	}
}

// TestLockModeConstants verifies the LockMode iota values are stable.
// These values may be persisted in config files, so they must not change.
func TestLockModeConstants(t *testing.T) {
	if LockModeNone != 0 {
		t.Errorf("LockModeNone = %d, want 0", LockModeNone)
	}
	if LockModeManual != 1 {
		t.Errorf("LockModeManual = %d, want 1", LockModeManual)
	}
	if LockModeAuto != 2 {
		t.Errorf("LockModeAuto = %d, want 2", LockModeAuto)
	}
}

// TestVTConstants verifies the ioctl constants match the kernel headers.
// These are verified against /usr/include/linux/vt.h on this machine.
func TestVTConstants(t *testing.T) {
	if vtSetMode != 0x5602 {
		t.Errorf("vtSetMode = 0x%04x, want 0x5602", vtSetMode)
	}
	if vtRelDisp != 0x5605 {
		t.Errorf("vtRelDisp = 0x%04x, want 0x5605", vtRelDisp)
	}
	if vtModeAuto != 0x00 {
		t.Errorf("vtModeAuto = 0x%02x, want 0x00", vtModeAuto)
	}
	if vtModeProcess != 0x01 {
		t.Errorf("vtModeProcess = 0x%02x, want 0x01", vtModeProcess)
	}
}

// TestOpenVT_NonExistentPath verifies OpenVT returns an error for a
// non-existent device path.
func TestOpenVT_NonExistentPath(t *testing.T) {
	_, err := OpenVT("/dev/tty-does-not-exist-99999")
	if err == nil {
		t.Error("OpenVT() should return error for non-existent path")
	}
}

// TestOpenVT_RealVT exercises OpenVT against an actual VT device.
// Only runs if the SD_TEST_VT environment variable is set (e.g. SD_TEST_VT=/dev/tty8).
// VT ioctls (LockVT, UnlockVT, AckRelease) require a real VT — they return
// ENOTTY on ptys.
func TestOpenVT_RealVT(t *testing.T) {
	vtPath := os.Getenv("SD_TEST_VT")
	if vtPath == "" {
		t.Skip("SD_TEST_VT not set; skipping real VT device test")
	}

	f, err := OpenVT(vtPath)
	if err != nil {
		t.Fatalf("OpenVT(%q) error: %v", vtPath, err)
	}
	defer f.Close()

	if f == nil {
		t.Error("OpenVT() returned nil file without error")
	}
}

// TestLockUnlockVT_RealVT exercises LockVT and UnlockVT against a real VT.
// Skipped unless SD_TEST_VT is set. These ioctls return ENOTTY on ptys.
func TestLockUnlockVT_RealVT(t *testing.T) {
	vtPath := os.Getenv("SD_TEST_VT")
	if vtPath == "" {
		t.Skip("SD_TEST_VT not set; skipping VT_SETMODE test (requires real VT device)")
	}

	f, err := OpenVT(vtPath)
	if err != nil {
		t.Fatalf("OpenVT(%q) error: %v", vtPath, err)
	}
	defer func() {
		// Always restore VT_AUTO before closing.
		_ = UnlockVT(f.Fd())
		f.Close()
	}()

	if err := LockVT(f.Fd()); err != nil {
		t.Fatalf("LockVT() error: %v", err)
	}
	if err := UnlockVT(f.Fd()); err != nil {
		t.Fatalf("UnlockVT() error: %v", err)
	}
}
