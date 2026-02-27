package procchain

import (
	"os"
	"testing"
)

func TestWalk_SelfProcess(t *testing.T) {
	pid := uint32(os.Getpid())
	chain := Walk(pid, 5)

	if len(chain) == 0 {
		t.Fatal("Walk() returned empty slice for current process")
	}

	first := chain[0]
	if first.PID != pid {
		t.Errorf("chain[0].PID = %d, want %d", first.PID, pid)
	}
	if first.Name == "" {
		t.Error("chain[0].Name should be non-empty")
	}
	// PPid should be non-zero for a regular process (not PID 1).
	if first.PPid == 0 {
		t.Error("chain[0].PPid should be non-zero for test process")
	}
}

func TestWalk_MaxDepth1(t *testing.T) {
	pid := uint32(os.Getpid())
	chain := Walk(pid, 1)

	if len(chain) != 1 {
		t.Errorf("Walk(pid, 1) returned %d entries, want exactly 1", len(chain))
	}
}

func TestWalk_MaxDepth0(t *testing.T) {
	pid := uint32(os.Getpid())
	chain := Walk(pid, 0)

	if len(chain) != 0 {
		t.Errorf("Walk(pid, 0) returned %d entries, want 0", len(chain))
	}
}

func TestWalk_InvalidPID(t *testing.T) {
	// PID 999999999 is extremely unlikely to exist.
	chain := Walk(999999999, 5)
	if len(chain) != 0 {
		t.Errorf("Walk(999999999, 5) returned %d entries, want 0", len(chain))
	}
}

func TestWalk_ZeroPID(t *testing.T) {
	// PID 0 should return empty (stopped by current <= 1 check).
	chain := Walk(0, 5)
	if len(chain) != 0 {
		t.Errorf("Walk(0, 5) returned %d entries, want 0", len(chain))
	}
}

func TestWalk_PID1(t *testing.T) {
	// PID 1 should return empty (the loop condition stops at <= 1).
	chain := Walk(1, 5)
	if len(chain) != 0 {
		t.Errorf("Walk(1, 5) returned %d entries, want 0", len(chain))
	}
}

func TestWalk_ChainTerminates(t *testing.T) {
	// Verify the chain does not loop back to a previously-seen PID.
	pid := uint32(os.Getpid())
	chain := Walk(pid, 10)

	seen := make(map[uint32]bool)
	for _, info := range chain {
		if seen[info.PID] {
			t.Errorf("cycle detected: PID %d appears more than once in chain", info.PID)
		}
		seen[info.PID] = true
	}
}

func TestWalk_AllEntriesHaveNames(t *testing.T) {
	pid := uint32(os.Getpid())
	chain := Walk(pid, 5)

	for i, info := range chain {
		if info.Name == "" {
			t.Errorf("chain[%d] (PID=%d) has empty Name", i, info.PID)
		}
	}
}

func TestParsePPid(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    uint32
		wantErr bool
	}{
		{
			name:  "standard status file excerpt",
			input: "Name:\tbash\nPPid:\t12345\nUid:\t1000 1000 1000 1000\n",
			want:  12345,
		},
		{
			name:  "ppid zero",
			input: "PPid:\t0\n",
			want:  0,
		},
		{
			name:    "missing ppid field",
			input:   "Name:\tfoo\nUid:\t0\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePPid([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parsePPid() = %d, want %d", got, tc.want)
			}
		})
	}
}
