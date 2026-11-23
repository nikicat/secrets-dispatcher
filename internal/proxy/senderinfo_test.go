package proxy

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// A PID above Linux's PID_MAX_LIMIT (4194304), without a /proc entry. The
// resolver walks the real /proc chain of the mocked PID, so a small fixed PID
// could name a real process, such as a test browser in a container.
const absentPID procutil.PID = 1 << 30

// Answers sender queries with fixed results.
type mockDBusClient struct {
	pid      procutil.PID
	pidErr   error
	uid      procutil.UID
	uidErr   error
	unitName systemd.Unit
	unitErr  error
}

func (m *mockDBusClient) connectionPID(name sender.UniqueName) (procutil.PID, error) {
	return m.pid, m.pidErr
}

func (m *mockDBusClient) connectionUser(name sender.UniqueName) (procutil.UID, error) {
	return m.uid, m.uidErr
}

func (m *mockDBusClient) unitByPID(pid procutil.PID) (systemd.Unit, error) {
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
	assert.Equal(t, absentPID, info.PID)
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
	assert.Equal(t, absentPID, info.PID)
	assert.Equal(t, new(procutil.UID(1000)), info.UID)
	assert.Empty(t, info.SystemdUnit)
}

func TestSenderInfoResolver_Resolve_PIDFails(t *testing.T) {
	client := &mockDBusClient{
		pidErr:   errors.New("connection not found"),
		uid:      1000,
		unitName: "test.service",
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	assert.Equal(t, new(sender.UniqueName(":1.123")), info.Sender)
	assert.Zero(t, info.PID)
	assert.Equal(t, new(procutil.UID(1000)), info.UID)
	assert.Empty(t, info.SystemdUnit, "unit lookup is skipped without a PID")
}

func TestSenderInfoResolver_Resolve_UIDFails(t *testing.T) {
	client := &mockDBusClient{
		pid:      absentPID,
		uidErr:   errors.New("connection not found"),
		unitName: "test.service",
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	assert.Equal(t, absentPID, info.PID)
	assert.Nil(t, info.UID, "an unreported UID stays unknown, not root")
	assert.Equal(t, systemd.Unit("test.service"), info.SystemdUnit)
}

func TestSenderInfoResolver_Resolve_AllFail(t *testing.T) {
	client := &mockDBusClient{
		pidErr:   errors.New("connection not found"),
		uidErr:   errors.New("connection not found"),
		unitName: "test.service",
	}
	resolver := newSenderInfoResolverWithClient(client)

	info := resolver.Resolve(":1.123")

	// Should still return sender, with zeros for everything else
	assert.Equal(t, new(sender.UniqueName(":1.123")), info.Sender)
	assert.Zero(t, info.PID)
	assert.Nil(t, info.UID, "an unreported UID stays unknown, not root")
	assert.Empty(t, info.SystemdUnit)
}

func TestNewSenderInfoResolver(t *testing.T) {
	// Test that newSenderInfoResolver creates a valid resolver
	resolver := newSenderInfoResolver(nil, approval.ClientLocal, nil)
	require.NotNil(t, resolver)
	assert.NotNil(t, resolver.client)
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
	require.NotEmpty(t, info.ProcessChain)
	self := info.ProcessChain[0]
	_, ok = self.ExePath()
	assert.True(t, ok, "expected an Exe in first ProcessChain entry")
	assert.NotEmpty(t, self.Args, "first ProcessChain entry has no Args")
	_, ok = self.CWDPath()
	assert.True(t, ok, "expected a CWD in first ProcessChain entry")
}

func TestSenderInfoResolver_Resolve_FiltersSelfExe(t *testing.T) {
	selfPID := procutil.PID(os.Getpid())
	exe, err := os.Executable()
	require.NoError(t, err)
	selfExe := procutil.Path(exe)
	hasSelf := func(chain []procutil.Process) bool {
		return slices.ContainsFunc(chain, func(p procutil.Process) bool {
			got, ok := p.ExePath()
			return ok && got == selfExe
		})
	}

	client := &mockDBusClient{pid: selfPID, uid: 1000}
	resolver := newSenderInfoResolverWithClient(client)

	// A zero ChainReader leaves the test binary in the chain.
	info := resolver.Resolve(":1.99")
	require.True(t, hasSelf(info.ProcessChain), "expected test binary in unfiltered chain")

	// With our own executable known, our binary should be filtered out.
	reader, err := sender.NewChainReader(procutil.ChainToInit)
	require.NoError(t, err)
	resolver.local = &reader
	info = resolver.Resolve(":1.99")
	assert.False(t, hasSelf(info.ProcessChain), "self exe %s should have been filtered from chain", selfExe)
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
	assert.Equal(t, absentPID, info.PID)
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
