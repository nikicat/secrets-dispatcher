package proxy

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// Stands in for gnome-shell's prompter on the front bus, recording the calls
// that the bridge forwards to it.
type mockShellPrompter struct {
	begin   chan promptCall
	perform chan promptCall
	stop    chan promptCall
}

type promptCall struct {
	callback dbus.ObjectPath
	sender   string // unique name that made the call (the bridge's front conn)
}

func (m *mockShellPrompter) BeginPrompting(msg dbus.Message, callback dbus.ObjectPath) *dbus.Error {
	m.begin <- promptCall{callback, string(senderOf(msg))}
	return nil
}

func (m *mockShellPrompter) PerformPrompt(msg dbus.Message, callback dbus.ObjectPath, promptType string, properties map[string]dbus.Variant, exchange string) *dbus.Error {
	m.perform <- promptCall{callback, string(senderOf(msg))}
	return nil
}

func (m *mockShellPrompter) StopPrompting(msg dbus.Message, callback dbus.ObjectPath) *dbus.Error {
	m.stop <- promptCall{callback, string(senderOf(msg))}
	return nil
}

// Stands in for the backend gnome-keyring's callback object, recording the
// PromptReady and PromptDone calls that the bridge forwards back.
type mockKeyringCallback struct {
	ready chan string
	done  chan struct{}
}

func (m *mockKeyringCallback) PromptReady(msg dbus.Message, reply string, properties map[string]dbus.Variant, exchange string) *dbus.Error {
	m.ready <- reply
	return nil
}

func (m *mockKeyringCallback) PromptDone(msg dbus.Message) *dbus.Error {
	close(m.done)
	return nil
}

// Checks that the bridge, holding SystemPrompter on the backend bus, carries the
// unlock prompt to gnome-shell's prompter on the session bus (ADR 0009) in both
// directions: Begin/Perform/StopPrompting to it and PromptReady/PromptDone back.
func TestPrompterBridge(t *testing.T) {
	frontAddr, backendAddr := testutil.StartDBusDaemon(t), testutil.StartDBusDaemon(t)

	// gnome-shell's prompter on the front bus, present before the proxy starts.
	shellConn, err := dbus.Connect(frontAddr)
	require.NoError(t, err)
	t.Cleanup(func() { shellConn.Close() })
	shell := &mockShellPrompter{
		begin:   make(chan promptCall, 1),
		perform: make(chan promptCall, 1),
		stop:    make(chan promptCall, 1),
	}
	require.NoError(t, shellConn.Export(shell, systemPrompterPath, string(prompterInterface)))
	claimTestName(t, shellConn, systemPrompterName)

	// The proxy (and thus the bridge) between the two buses.
	p := startSessionBusTestProxy(t, frontAddr, backendAddr)
	require.True(t, p.conn.prompter.active, "bridge should claim the prompter name on the backend bus")

	// The backend gnome-keyring: a connection that exports a callback object
	// and calls the SystemPrompter (now the bridge) on the backend bus.
	keyringConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err)
	t.Cleanup(func() { keyringConn.Close() })
	cb := &mockKeyringCallback{ready: make(chan string, 2), done: make(chan struct{})}
	const callbackPath = dbus.ObjectPath("/org/gnome/keyring/Prompt/test")
	require.NoError(t, keyringConn.Export(cb, callbackPath, string(prompterCallbackInterface)))

	prompter := keyringConn.Object(systemPrompterName, dbus.ObjectPath(systemPrompterPath))

	// keyring -> (bridge) -> shell: BeginPrompting.
	require.NoError(t, prompter.Call(prompterInterface.Member("BeginPrompting"), 0, callbackPath).Err)
	begin := recvCall(t, shell.begin, "BeginPrompting")
	assert.Equal(t, callbackPath, begin.callback)

	// shell -> (bridge) -> keyring: PromptReady on the callback the bridge
	// exported on the front bus (addressed to the bridge's front conn, the
	// sender of BeginPrompting).
	bridgeFront := shellConn.Object(begin.sender, callbackPath)
	require.NoError(t, bridgeFront.Call(prompterCallbackInterface.Member("PromptReady"), 0, "", map[string]dbus.Variant{}, "exchange-a").Err)
	assert.Equal(t, "", recvReply(t, cb.ready), "empty ready reply must reach the keyring callback")

	// keyring -> shell: PerformPrompt (verbatim, exchange included).
	require.NoError(t, prompter.Call(prompterInterface.Member("PerformPrompt"), 0,
		callbackPath, "password", map[string]dbus.Variant{}, "exchange-b").Err)
	assert.Equal(t, callbackPath, recvCall(t, shell.perform, "PerformPrompt").callback)

	// shell -> keyring: the user's answer, then PromptDone.
	require.NoError(t, bridgeFront.Call(prompterCallbackInterface.Member("PromptReady"), 0, "yes", map[string]dbus.Variant{}, "exchange-c").Err)
	assert.Equal(t, "yes", recvReply(t, cb.ready))
	require.NoError(t, bridgeFront.Call(prompterCallbackInterface.Member("PromptDone"), 0).Err)
	select {
	case <-cb.done:
	case <-time.After(5 * time.Second):
		t.Fatal("PromptDone was not forwarded to the keyring callback")
	}

	// keyring -> shell: StopPrompting ends the exchange.
	require.NoError(t, prompter.Call(prompterInterface.Member("StopPrompting"), 0, callbackPath).Err)
	assert.Equal(t, callbackPath, recvCall(t, shell.stop, "StopPrompting").callback)
}

// Checks that the bridge claims SystemPrompter on the backend bus before
// gnome-shell registers its prompter, so the gcr-prompter fallback cannot win a
// relogin race, and forwards to gnome-shell once it is up (ADR 0009).
func TestPrompterBridgeClaimsBackendBeforePrompter(t *testing.T) {
	frontAddr, backendAddr := testutil.StartDBusDaemon(t), testutil.StartDBusDaemon(t)

	// Connect the proxy with NO front prompter present yet.
	p := startSessionBusTestProxy(t, frontAddr, backendAddr)

	// It must own the backend name straight away — before any front prompter —
	// so the gcr-prompter fallback has no window to claim it.
	require.True(t, p.conn.prompter.active, "bridge should activate when the backend bus has no prompter")
	assert.True(t, busDaemon{p.conn.h.backendConn}.nameHasOwner(systemPrompterName),
		"bridge must claim the backend name at startup, before a front prompter exists")

	// gnome-shell's prompter appears on the front bus (as after a relogin).
	shellConn, err := dbus.Connect(frontAddr)
	require.NoError(t, err)
	t.Cleanup(func() { shellConn.Close() })
	shell := &mockShellPrompter{
		begin:   make(chan promptCall, 1),
		perform: make(chan promptCall, 1),
		stop:    make(chan promptCall, 1),
	}
	require.NoError(t, shellConn.Export(shell, systemPrompterPath, string(prompterInterface)))
	claimTestName(t, shellConn, systemPrompterName)

	// Forwarding must work end-to-end once the front prompter is up — resolved
	// by name at call time, no re-arming needed.
	keyringConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err)
	t.Cleanup(func() { keyringConn.Close() })
	prompter := keyringConn.Object(systemPrompterName, dbus.ObjectPath(systemPrompterPath))
	require.NoError(t, prompter.Call(prompterInterface.Member("BeginPrompting"), 0, dbus.ObjectPath("/org/gnome/keyring/Prompt/x")).Err)
	recvCall(t, shell.begin, "BeginPrompting")
}

// A proxy that serves a forwarded bus reaches the backend on the user's session
// bus. The bridge must not claim the prompter's name there, or it blocks the
// prompter that the session bus activates for its own clients.
func TestPrompterBridgeLeavesSessionBusBackendAlone(t *testing.T) {
	forwardedAddr, sessionAddr := testutil.StartDBusDaemon(t), testutil.StartDBusDaemon(t)

	p := startTestProxy(t, forwardedAddr, sessionAddr)

	assert.False(t, busDaemon{p.conn.h.backendConn}.nameHasOwner(systemPrompterName),
		"bridge must not claim the prompter's name on the session bus")
}

func recvCall(t *testing.T, ch chan promptCall, what string) promptCall {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s to reach the shell prompter", what)
		return promptCall{}
	}
}

func recvReply(t *testing.T, ch chan string) string {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for PromptReady to reach the keyring callback")
		return ""
	}
}
