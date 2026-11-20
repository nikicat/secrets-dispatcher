package proxy

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// absentPID is above Linux's PID_MAX_LIMIT (4194304), so no /proc entry exists for it: the
// resolver walks the real /proc chain of the mocked PID, and a real process with a fixed small
// PID (a test browser in a container's PID namespace, say) would be resolved instead.
const absentPID = 1 << 30

// mockDBusClient implements dbusClient for testing.
type mockDBusClient struct {
	pid      procutil.PID
	pidErr   error
	uid      procutil.UID
	uidErr   error
	unitName systemd.Unit
	unitErr  error
}

func (m *mockDBusClient) GetConnectionUnixProcessID(sender sender.UniqueName) (procutil.PID, error) {
	return m.pid, m.pidErr
}

func (m *mockDBusClient) GetConnectionUnixUser(sender sender.UniqueName) (procutil.UID, error) {
	return m.uid, m.uidErr
}

func (m *mockDBusClient) GetUnitByPID(pid procutil.PID) (systemd.Unit, error) {
	return m.unitName, m.unitErr
}

func TestSenderInfoResolver_Resolve_AllSuccess(t *testing.T) {
	client := &mockDBusClient{
		pid:      absentPID,
		uid:      1000,
		unitName: "test.service",
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	assert.Equal(t, new(sender.UniqueName(":1.123")), info.Sender)
	if info.PID != absentPID {
		t.Errorf("expected PID %d, got %d", absentPID, info.PID)
	}
	assert.Equal(t, new(procutil.UID(1000)), info.UID)
	assert.Empty(t, info.InvokerName, "no /proc chain, so no process name")
	assert.Equal(t, systemd.Unit("test.service"), info.SystemdUnit)
}

func TestSenderInfoResolver_Resolve_NoSystemd(t *testing.T) {
	// Simulates remote host without systemd or process not in a unit
	client := &mockDBusClient{
		pid:     absentPID,
		uid:     1000,
		unitErr: errors.New("the PID does not belong to any loaded unit"),
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	assert.Equal(t, new(sender.UniqueName(":1.123")), info.Sender)
	if info.PID != absentPID {
		t.Errorf("expected PID %d, got %d", absentPID, info.PID)
	}
	assert.Equal(t, new(procutil.UID(1000)), info.UID)
	if info.InvokerName != "" {
		t.Errorf("expected empty unit_name, got %s", info.InvokerName)
	}
}

func TestSenderInfoResolver_Resolve_PIDFails(t *testing.T) {
	client := &mockDBusClient{
		pidErr: errors.New("connection not found"),
		uid:    1000,
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	assert.Equal(t, new(sender.UniqueName(":1.123")), info.Sender)
	if info.PID != 0 {
		t.Errorf("expected PID 0, got %d", info.PID)
	}
	assert.Equal(t, new(procutil.UID(1000)), info.UID)
	// Unit lookup should be skipped when PID is 0
	if info.InvokerName != "" {
		t.Errorf("expected empty unit_name, got %s", info.InvokerName)
	}
}

func TestSenderInfoResolver_Resolve_UIDFails(t *testing.T) {
	client := &mockDBusClient{
		pid:      absentPID,
		uidErr:   errors.New("connection not found"),
		unitName: "test.service",
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	if info.PID != absentPID {
		t.Errorf("expected PID %d, got %d", absentPID, info.PID)
	}
	assert.Nil(t, info.UID, "an unreported UID stays unknown, not root")
	assert.Equal(t, systemd.Unit("test.service"), info.SystemdUnit)
}

func TestSenderInfoResolver_Resolve_AllFail(t *testing.T) {
	client := &mockDBusClient{
		pidErr: errors.New("connection not found"),
		uidErr: errors.New("connection not found"),
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	// Should still return sender, with zeros for everything else
	assert.Equal(t, new(sender.UniqueName(":1.123")), info.Sender)
	if info.PID != 0 {
		t.Errorf("expected PID 0, got %d", info.PID)
	}
	assert.Nil(t, info.UID, "an unreported UID stays unknown, not root")
	if info.InvokerName != "" {
		t.Errorf("expected empty unit_name, got %s", info.InvokerName)
	}
}

func TestSenderInfoResolver_Resolve_PartialInfo(t *testing.T) {
	// Verify that SenderInfo struct is properly initialized
	info := approval.SenderInfo{
		Sender:      new(sender.UniqueName(":1.456")),
		PID:         12345,
		UID:         new(procutil.UID(1000)),
		InvokerName: "gh",
	}

	assert.Equal(t, new(sender.UniqueName(":1.456")), info.Sender)
	if info.PID != 12345 {
		t.Errorf("expected PID 12345, got %d", info.PID)
	}
	assert.Equal(t, new(procutil.UID(1000)), info.UID)
	if info.InvokerName != "gh" {
		t.Errorf("expected invoker gh, got %s", info.InvokerName)
	}
}

func TestNewSenderInfoResolver(t *testing.T) {
	// Test that newSenderInfoResolver creates a valid resolver
	resolver := newSenderInfoResolver(nil, approval.ClientLocal, nil)
	if resolver == nil {
		t.Fatal("newSenderInfoResolver returned nil")
	}
	if resolver.client == nil {
		t.Error("expected non-nil client")
	}
}

func TestSenderInfoResolver_Resolve_InvokerResolution(t *testing.T) {
	// Use our own PID so /proc reading works and procutil resolves the invoker.
	selfPID := procutil.PID(os.Getpid())
	client := &mockDBusClient{
		pid:      selfPID,
		uid:      1000,
		unitName: "should-not-be-used.service",
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.42")

	// procutil should have resolved the invoker, so InvokerName is the process comm.
	invoker, ok := selfPID.Invoker()
	require.True(t, ok)
	assert.Equal(t, invoker.Name, info.InvokerName, "InvokerName should be the process comm from procutil")
	assert.Equal(t, invoker.PID, info.PID, "PID should be the invoker PID from procutil")
	// The systemd unit is resolved separately and never overwrites the comm display.
	assert.NotEqual(t, "should-not-be-used.service", info.InvokerName, "systemd unit must not leak into InvokerName")
	assert.Equal(t, systemd.Unit("should-not-be-used.service"), info.SystemdUnit, "SystemdUnit should be resolved from GetUnitByPID")

	// Verify process chain entries have Exe/Args/CWD populated.
	if len(info.ProcessChain) == 0 {
		t.Fatal("expected non-empty ProcessChain")
	}
	self := info.ProcessChain[0]
	if self.Exe == nil {
		t.Error("expected an Exe in first ProcessChain entry")
	}
	if len(self.Args) == 0 {
		t.Error("expected non-empty Args in first ProcessChain entry")
	}
	if self.CWD == nil {
		t.Error("expected a CWD in first ProcessChain entry")
	}
}

func TestSenderInfoResolver_Resolve_FiltersSelfExe(t *testing.T) {
	selfPID := procutil.PID(os.Getpid())
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	selfExe := procutil.Path(exe)

	client := &mockDBusClient{pid: selfPID, uid: 1000}
	resolver := newSenderInfoResolverWithClient(client)

	// A zero ChainReader leaves the test binary in the chain.
	info := resolver.Resolve(":1.99")
	var foundSelf bool
	for _, p := range info.ProcessChain {
		if p.Exe != nil && *p.Exe == selfExe {
			foundSelf = true
			break
		}
	}
	if !foundSelf {
		t.Fatal("expected test binary in unfiltered chain")
	}

	// With our own executable known, our binary should be filtered out.
	reader, err := sender.NewChainReader(procutil.ChainToInit)
	require.NoError(t, err)
	resolver.local = &reader
	info = resolver.Resolve(":1.99")
	for _, p := range info.ProcessChain {
		if p.Exe != nil && *p.Exe == selfExe {
			t.Errorf("self exe %q should have been filtered from chain", selfExe)
		}
	}
}

func TestSenderInfoResolver_Resolve_FallbackToSystemd(t *testing.T) {
	// absentPID has no /proc entry, so the display falls back to the unit from
	// systemd GetUnitByPID.
	client := &mockDBusClient{
		pid:      absentPID,
		uid:      1000,
		unitName: "fallback.service",
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.42")

	assert.Empty(t, info.InvokerName)
	assert.Equal(t, "fallback.service", info.DisplayName())
	// PID should remain as the original D-Bus PID when procutil fails.
	if info.PID != absentPID {
		t.Errorf("expected PID %d, got %d", absentPID, info.PID)
	}
}

// A forwarded PID that also exists here, as this test's own does, must not lend
// its process chain or unit to the request.
func TestSenderInfoResolver_Resolve_Forwarded(t *testing.T) {
	client := &mockDBusClient{pid: procutil.PID(os.Getpid()), uid: 1000, unitName: "local.service"}
	resolver := newSenderInfoResolverWithClient(client)
	resolver.clientName = "symbiosis"
	resolver.local = nil

	info := resolver.Resolve(":1.7")

	assert.Equal(t, approval.ClientName("symbiosis"), info.Client)
	assert.Equal(t, procutil.PID(os.Getpid()), info.PID)
	assert.Equal(t, new(procutil.UID(1000)), info.UID)
	assert.Empty(t, info.ProcessChain)
	assert.Empty(t, info.SystemdUnit)
	assert.Empty(t, info.InvokerName)
}
