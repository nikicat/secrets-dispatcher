package proxy

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCollectionPath = dbus.ObjectPath("/org/freedesktop/secrets/collection/default")

// Subscribes the client to Prompt.Completed on the front bus and returns the
// channel the signals arrive on.
func (e *mockServiceEnv) subscribeCompleted(t *testing.T) chan *dbus.Signal {
	t.Helper()
	require.NoError(t, e.client.AddMatchSignal(
		dbus.WithMatchInterface(string(dbustypes.PromptInterface)),
		dbus.WithMatchMember("Completed"),
	))
	ch := make(chan *dbus.Signal, 8)
	e.client.Signal(ch)
	return ch
}

// Asks the proxy to unlock the locked default collection and returns the
// prompt that the backend requires first.
func (e *mockServiceEnv) startUnlock(t *testing.T) dbus.ObjectPath {
	t.Helper()
	var unlocked []dbus.ObjectPath
	var promptPath dbus.ObjectPath
	err := e.client.Object(dbustypes.BusName, dbustypes.ServicePath).
		Call(dbustypes.ServiceInterface.Member("Unlock"), 0, []dbus.ObjectPath{testCollectionPath}).
		Store(&unlocked, &promptPath)
	require.NoError(t, err, "Unlock via proxy")
	require.Empty(t, unlocked, "locked collection must not unlock without a prompt")
	require.True(t, dbustypes.IsPromptPath(promptPath), "Unlock should return a prompt path, got %q", promptPath)
	return promptPath
}

func waitCompleted(t *testing.T, ch chan *dbus.Signal, promptPath dbus.ObjectPath) *dbus.Signal {
	t.Helper()
	for {
		select {
		case sig := <-ch:
			if sig.Path != promptPath || sig.Name != dbustypes.PromptInterface.Member("Completed") {
				continue
			}
			return sig
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for Prompt.Completed signal on the front bus")
			return nil
		}
	}
}

// Regression test for issue #1. A client calls Prompt, through the proxy, on
// the path that Unlock returns for a locked collection, and sees Completed.
func TestPromptForwardingUnlockFlow(t *testing.T) {
	env := newMockServiceEnv(t)
	itemPath := env.mock.AddItem("Locked Secret", map[string]string{"app": "prompt-test"}, []byte("hunter2"))
	env.mock.SetLocked(true)

	completed := env.subscribeCompleted(t)
	promptPath := env.startUnlock(t)

	err := env.client.Object(dbustypes.BusName, promptPath).
		Call(dbustypes.PromptInterface.Member("Prompt"), 0, "window-42").Err
	require.NoError(t, err, "Prompt call must be forwarded to the backend")

	sig := waitCompleted(t, completed, promptPath)
	require.Len(t, sig.Body, 2)
	assert.Equal(t, false, sig.Body[0], "dismissed flag")
	result, ok := sig.Body[1].(dbus.Variant)
	require.True(t, ok, "Completed result must be a variant, got %T", sig.Body[1])
	assert.Equal(t, []dbus.ObjectPath{testCollectionPath}, result.Value(), "unlocked objects")

	assert.Equal(t, "window-42", env.mock.LastWindowID(), "window ID must reach the backend prompt")
	assert.False(t, env.mock.Locked(), "collection must be unlocked after the prompt")

	// The point of prompt forwarding: after the prompt flow the secret is actually reachable.
	var output dbus.Variant
	var sessionPath dbus.ObjectPath
	err = env.client.Object(dbustypes.BusName, dbustypes.ServicePath).
		Call(dbustypes.ServiceInterface.Member("OpenSession"), 0, dbustypes.AlgorithmPlain, dbus.MakeVariant("")).
		Store(&output, &sessionPath)
	require.NoError(t, err, "OpenSession via proxy")

	var secrets map[dbus.ObjectPath]dbustypes.Secret
	err = env.client.Object(dbustypes.BusName, dbustypes.ServicePath).
		Call(dbustypes.ServiceInterface.Member("GetSecrets"), 0, []dbus.ObjectPath{itemPath}, sessionPath).
		Store(&secrets)
	require.NoError(t, err, "GetSecrets after unlock")
	require.Contains(t, secrets, itemPath)
	assert.Equal(t, []byte("hunter2"), secrets[itemPath].Value)
}

// Checks that Dismiss is forwarded and reported in Completed(dismissed=true)
// while the collection stays locked.
func TestPromptDismiss(t *testing.T) {
	env := newMockServiceEnv(t)
	env.mock.SetLocked(true)

	completed := env.subscribeCompleted(t)
	promptPath := env.startUnlock(t)

	err := env.client.Object(dbustypes.BusName, promptPath).
		Call(dbustypes.PromptInterface.Member("Dismiss"), 0).Err
	require.NoError(t, err, "Dismiss call must be forwarded to the backend")

	sig := waitCompleted(t, completed, promptPath)
	require.Len(t, sig.Body, 2)
	assert.Equal(t, true, sig.Body[0], "dismissed flag")

	assert.True(t, env.mock.Locked(), "collection must stay locked after dismissal")
}

// Checks that a Prompt call on PromptsPath itself, which names no prompt,
// fails with NoSuchObject.
func TestPromptOnNonPromptPath(t *testing.T) {
	env := newMockServiceEnv(t)

	err := env.client.Object(dbustypes.BusName, dbustypes.PromptsPath).
		Call(dbustypes.PromptInterface.Member("Prompt"), 0, "").Err
	require.Error(t, err)
	var dbusErr dbus.Error
	require.ErrorAs(t, err, &dbusErr)
	assert.Equal(t, string(dbustypes.NoSuchObject), dbusErr.Name)
}
