package proxy

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockShellPrompter stands in for gnome-shell's real prompter on the front bus:
// it owns org.gnome.keyring.SystemPrompter and records the calls the bridge
// forwards to it. It can also drive the reverse Callback.
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

// mockKeyringCallback stands in for the backend gnome-keyring's callback object:
// it records the PromptReady/PromptDone calls the bridge forwards back.
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

// TestPrompterBridge is the regression for the locked-keyring hang: the backend
// gnome-keyring reaches its unlock prompter over the private bus, where the
// only SystemPrompter is the display-less gcr-prompter fallback, so unlock
// hangs forever. The bridge must claim SystemPrompter on the backend bus and
// relay the whole two-way protocol to the real prompter (gnome-shell) on the
// front bus. This test drives both directions: keyring→shell (Begin/Perform/
// Stop) and shell→keyring (PromptReady/PromptDone).
func TestPrompterBridge(t *testing.T) {
	tmpDir := t.TempDir()

	backendCmd, backendAddr := startTestDBusDaemon(t, filepath.Join(tmpDir, "backend.sock"))
	t.Cleanup(func() { backendCmd.Process.Kill(); backendCmd.Wait() })
	frontCmd, frontAddr := startTestDBusDaemon(t, filepath.Join(tmpDir, "front.sock"))
	t.Cleanup(func() { frontCmd.Process.Kill(); frontCmd.Wait() })

	// gnome-shell's prompter on the front bus, present before the proxy starts
	// (so the bridge's guard sees it and activates).
	shellConn, err := dbus.Connect(frontAddr)
	require.NoError(t, err)
	t.Cleanup(func() { shellConn.Close() })
	shell := &mockShellPrompter{
		begin:   make(chan promptCall, 1),
		perform: make(chan promptCall, 1),
		stop:    make(chan promptCall, 1),
	}
	require.NoError(t, shellConn.Export(shell, systemPrompterPath, prompterInterface))
	reply, err := shellConn.RequestName(systemPrompterName, dbus.NameFlagDoNotQueue)
	require.NoError(t, err)
	require.Equal(t, dbus.RequestNameReplyPrimaryOwner, reply)

	// The proxy (and thus the bridge) between the two buses.
	proxyBackendConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err)
	proxyFrontConn, err := dbus.Connect(frontAddr)
	require.NoError(t, err)
	p := New(Config{ClientName: "prompter-test", LogLevel: slog.LevelDebug})
	require.NoError(t, p.ConnectWith(proxyFrontConn, proxyBackendConn))
	t.Cleanup(func() { p.Close() })
	require.NotNil(t, p.prompter, "bridge should activate when the front bus has a prompter and the backend does not")

	// The backend gnome-keyring: a connection that exports a callback object
	// and calls the SystemPrompter (now the bridge) on the backend bus.
	keyringConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err)
	t.Cleanup(func() { keyringConn.Close() })
	cb := &mockKeyringCallback{ready: make(chan string, 2), done: make(chan struct{})}
	const callbackPath = dbus.ObjectPath("/org/gnome/keyring/Prompt/test")
	require.NoError(t, keyringConn.Export(cb, callbackPath, prompterCallbackInterface))

	prompter := keyringConn.Object(systemPrompterName, dbus.ObjectPath(systemPrompterPath))

	// keyring -> (bridge) -> shell: BeginPrompting.
	require.NoError(t, prompter.Call(prompterInterface+".BeginPrompting", 0, callbackPath).Err)
	begin := recvCall(t, shell.begin, "BeginPrompting")
	assert.Equal(t, callbackPath, begin.callback)

	// shell -> (bridge) -> keyring: PromptReady on the callback the bridge
	// exported on the front bus (addressed to the bridge's front conn, the
	// sender of BeginPrompting).
	bridgeFront := shellConn.Object(begin.sender, callbackPath)
	require.NoError(t, bridgeFront.Call(prompterCallbackInterface+".PromptReady", 0, "", map[string]dbus.Variant{}, "exchange-a").Err)
	assert.Equal(t, "", recvReply(t, cb.ready), "empty ready reply must reach the keyring callback")

	// keyring -> shell: PerformPrompt (verbatim, exchange included).
	require.NoError(t, prompter.Call(prompterInterface+".PerformPrompt", 0,
		callbackPath, "password", map[string]dbus.Variant{}, "exchange-b").Err)
	assert.Equal(t, callbackPath, recvCall(t, shell.perform, "PerformPrompt").callback)

	// shell -> keyring: the user's answer, then PromptDone.
	require.NoError(t, bridgeFront.Call(prompterCallbackInterface+".PromptReady", 0, "yes", map[string]dbus.Variant{}, "exchange-c").Err)
	assert.Equal(t, "yes", recvReply(t, cb.ready))
	require.NoError(t, bridgeFront.Call(prompterCallbackInterface+".PromptDone", 0).Err)
	select {
	case <-cb.done:
	case <-time.After(5 * time.Second):
		t.Fatal("PromptDone was not forwarded to the keyring callback")
	}

	// keyring -> shell: StopPrompting ends the exchange.
	require.NoError(t, prompter.Call(prompterInterface+".StopPrompting", 0, callbackPath).Err)
	assert.Equal(t, callbackPath, recvCall(t, shell.stop, "StopPrompting").callback)
}

// TestPrompterBridgeInactiveWithoutShellPrompter verifies the bridge stays off
// when the front bus has no prompter to forward to (e.g. remote mode), so it
// never claims a name it can't service.
func TestPrompterBridgeInactiveWithoutShellPrompter(t *testing.T) {
	tmpDir := t.TempDir()
	backendCmd, backendAddr := startTestDBusDaemon(t, filepath.Join(tmpDir, "backend.sock"))
	t.Cleanup(func() { backendCmd.Process.Kill(); backendCmd.Wait() })
	frontCmd, frontAddr := startTestDBusDaemon(t, filepath.Join(tmpDir, "front.sock"))
	t.Cleanup(func() { frontCmd.Process.Kill(); frontCmd.Wait() })

	proxyBackendConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err)
	proxyFrontConn, err := dbus.Connect(frontAddr)
	require.NoError(t, err)
	p := New(Config{ClientName: "prompter-test", LogLevel: slog.LevelDebug})
	require.NoError(t, p.ConnectWith(proxyFrontConn, proxyBackendConn))
	t.Cleanup(func() { p.Close() })

	assert.Nil(t, p.prompter, "bridge must not activate without a front-bus prompter")
	assert.False(t, nameHasOwner(proxyBackendConn, systemPrompterName), "bridge must not claim the prompter name it cannot service")
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
