package proxy

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsPromptPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/org/freedesktop/secrets/prompt/p1", true},
		{"/org/freedesktop/secrets/prompt/u42", true},
		{"/org/freedesktop/secrets/prompt/sub/path", true},

		// The subtree root itself is not a prompt object
		{"/org/freedesktop/secrets/prompt", false},
		{"/org/freedesktop/secrets/prompt/", false},

		{"/org/freedesktop/secrets", false},
		{"/org/freedesktop/secrets/collection/default", false},
		{"/org/freedesktop/secrets/promptx/p1", false},
		{"/completely/different/path", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, tc.want, isPromptPath(dbus.ObjectPath(tc.path)))
		})
	}
}

const testCollectionPath = dbus.ObjectPath("/org/freedesktop/secrets/collection/default")

// promptTestEnv runs a mock Secret Service on a private backend bus and the
// proxy in front of it on a private front bus, mirroring the production
// topology (client -> front bus -> proxy -> backend bus -> real service).
type promptTestEnv struct {
	client *dbus.Conn
	mock   *testutil.MockSecretService
}

func newPromptTestEnv(t *testing.T) *promptTestEnv {
	t.Helper()
	tmpDir := t.TempDir()

	backendCmd, backendAddr := startTestDBusDaemon(t, filepath.Join(tmpDir, "backend.sock"))
	t.Cleanup(func() {
		backendCmd.Process.Kill()
		backendCmd.Wait()
	})

	frontCmd, frontAddr := startTestDBusDaemon(t, filepath.Join(tmpDir, "front.sock"))
	t.Cleanup(func() {
		frontCmd.Process.Kill()
		frontCmd.Wait()
	})

	mockConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err, "connect mock to backend bus")
	t.Cleanup(func() { mockConn.Close() })

	mock := testutil.NewMockSecretService()
	require.NoError(t, mock.Register(mockConn), "register mock service")

	proxyBackendConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err, "connect proxy to backend bus")
	proxyFrontConn, err := dbus.Connect(frontAddr)
	require.NoError(t, err, "connect proxy to front bus")

	p := New(Config{ClientName: "prompt-test", LogLevel: slog.LevelDebug})
	require.NoError(t, p.ConnectWith(proxyFrontConn, proxyBackendConn), "connect proxy")
	t.Cleanup(func() { p.Close() })

	client, err := dbus.Connect(frontAddr)
	require.NoError(t, err, "connect client to front bus")
	t.Cleanup(func() { client.Close() })

	return &promptTestEnv{client: client, mock: mock}
}

// subscribeCompleted subscribes the client to Prompt.Completed signals on the
// front bus and returns the delivery channel.
func (e *promptTestEnv) subscribeCompleted(t *testing.T) chan *dbus.Signal {
	t.Helper()
	require.NoError(t, e.client.AddMatchSignal(
		dbus.WithMatchInterface(dbustypes.PromptInterface),
		dbus.WithMatchMember("Completed"),
	))
	ch := make(chan *dbus.Signal, 8)
	e.client.Signal(ch)
	return ch
}

// unlockViaPrompt calls Service.Unlock for the default collection and returns
// the prompt path the backend requires.
func (e *promptTestEnv) unlockViaPrompt(t *testing.T) dbus.ObjectPath {
	t.Helper()
	var unlocked []dbus.ObjectPath
	var promptPath dbus.ObjectPath
	err := e.client.Object(dbustypes.BusName, dbustypes.ServicePath).
		Call(dbustypes.ServiceInterface+".Unlock", 0, []dbus.ObjectPath{testCollectionPath}).
		Store(&unlocked, &promptPath)
	require.NoError(t, err, "Unlock via proxy")
	require.Empty(t, unlocked, "locked collection must not unlock without a prompt")
	require.True(t, isPromptPath(promptPath), "Unlock should return a prompt path, got %q", promptPath)
	return promptPath
}

func waitCompleted(t *testing.T, ch chan *dbus.Signal, promptPath dbus.ObjectPath) *dbus.Signal {
	t.Helper()
	for {
		select {
		case sig := <-ch:
			if sig.Path != promptPath || sig.Name != dbustypes.PromptInterface+".Completed" {
				continue
			}
			return sig
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for Prompt.Completed signal on the front bus")
			return nil
		}
	}
}

// TestPromptForwardingUnlockFlow is the US-6 regression test: a locked
// collection returns a prompt path from Unlock, and the client must be able to
// call Prompt on that path *through the proxy* and see Completed. Before the
// PromptHandler existed the call failed with "Object does not implement
// org.freedesktop.Secret.Prompt", locking users out of their own secrets.
func TestPromptForwardingUnlockFlow(t *testing.T) {
	env := newPromptTestEnv(t)
	itemPath := env.mock.AddItem("Locked Secret", map[string]string{"app": "prompt-test"}, []byte("hunter2"))
	env.mock.SetLocked(true)

	completed := env.subscribeCompleted(t)
	promptPath := env.unlockViaPrompt(t)

	err := env.client.Object(dbustypes.BusName, promptPath).
		Call(dbustypes.PromptInterface+".Prompt", 0, "window-42").Err
	require.NoError(t, err, "Prompt call must be forwarded to the backend")

	sig := waitCompleted(t, completed, promptPath)
	require.Len(t, sig.Body, 2)
	assert.Equal(t, false, sig.Body[0], "dismissed flag")
	result, ok := sig.Body[1].(dbus.Variant)
	require.True(t, ok, "Completed result must be a variant, got %T", sig.Body[1])
	assert.Equal(t, []dbus.ObjectPath{testCollectionPath}, result.Value(), "unlocked objects")

	assert.Equal(t, "window-42", env.mock.LastWindowID(), "window ID must reach the backend prompt")
	assert.False(t, env.mock.Locked(), "collection must be unlocked after the prompt")

	// The point of US-6: after the prompt flow the secret is actually reachable.
	var output dbus.Variant
	var sessionPath dbus.ObjectPath
	err = env.client.Object(dbustypes.BusName, dbustypes.ServicePath).
		Call(dbustypes.ServiceInterface+".OpenSession", 0, dbustypes.AlgorithmPlain, dbus.MakeVariant("")).
		Store(&output, &sessionPath)
	require.NoError(t, err, "OpenSession via proxy")

	var secrets map[dbus.ObjectPath]dbustypes.Secret
	err = env.client.Object(dbustypes.BusName, dbustypes.ServicePath).
		Call(dbustypes.ServiceInterface+".GetSecrets", 0, []dbus.ObjectPath{itemPath}, sessionPath).
		Store(&secrets)
	require.NoError(t, err, "GetSecrets after unlock")
	require.Contains(t, secrets, itemPath)
	assert.Equal(t, []byte("hunter2"), secrets[itemPath].Value)
}

// TestPromptDismiss verifies Dismiss is forwarded and the dismissal is
// reported via Completed(dismissed=true) while the collection stays locked.
func TestPromptDismiss(t *testing.T) {
	env := newPromptTestEnv(t)
	env.mock.SetLocked(true)

	completed := env.subscribeCompleted(t)
	promptPath := env.unlockViaPrompt(t)

	err := env.client.Object(dbustypes.BusName, promptPath).
		Call(dbustypes.PromptInterface+".Dismiss", 0).Err
	require.NoError(t, err, "Dismiss call must be forwarded to the backend")

	sig := waitCompleted(t, completed, promptPath)
	require.Len(t, sig.Body, 2)
	assert.Equal(t, true, sig.Body[0], "dismissed flag")

	assert.True(t, env.mock.Locked(), "collection must stay locked after dismissal")
}

// TestPromptOnNonPromptPath verifies the handler rejects paths outside the
// prompt namespace (here: the subtree root itself) with NoSuchObject.
func TestPromptOnNonPromptPath(t *testing.T) {
	env := newPromptTestEnv(t)

	err := env.client.Object(dbustypes.BusName, "/org/freedesktop/secrets/prompt").
		Call(dbustypes.PromptInterface+".Prompt", 0, "").Err
	require.Error(t, err)
	var dbusErr dbus.Error
	require.ErrorAs(t, err, &dbusErr)
	assert.Equal(t, dbustypes.ErrNoSuchObject, dbusErr.Name)
}
