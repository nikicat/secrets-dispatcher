package proxy

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const registryTestPrompt = dbus.ObjectPath("/org/freedesktop/secrets/prompt/reused")

type promptRegistryTestEnv struct {
	registry *promptRegistry
	tracker  *clientTracker
	clientA  *dbus.Conn
	clientB  *dbus.Conn
	ownerA   senderName
	ownerB   senderName
}

func newPromptRegistryTestEnv(t *testing.T) *promptRegistryTestEnv {
	t.Helper()
	cmd, addr := startTestDBusDaemon(t, filepath.Join(t.TempDir(), "registry.sock"))
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	trackerConn, err := dbus.Connect(addr)
	require.NoError(t, err)
	t.Cleanup(func() { trackerConn.Close() })
	tracker, err := newClientTracker(trackerConn)
	require.NoError(t, err)
	t.Cleanup(tracker.close)

	clientA, err := dbus.Connect(addr)
	require.NoError(t, err)
	t.Cleanup(func() { clientA.Close() })
	clientB, err := dbus.Connect(addr)
	require.NoError(t, err)
	t.Cleanup(func() { clientB.Close() })

	registry := newPromptRegistry(tracker)
	t.Cleanup(registry.close)
	return &promptRegistryTestEnv{
		registry: registry,
		tracker:  tracker,
		clientA:  clientA,
		clientB:  clientB,
		ownerA:   senderName(clientA.Names()[0]),
		ownerB:   senderName(clientB.Names()[0]),
	}
}

func TestPromptRegistryValidationAndConflict(t *testing.T) {
	env := newPromptRegistryTestEnv(t)

	require.NoError(t, env.registry.register("/", env.ownerA))
	_, ok := env.registry.lookup("/")
	assert.False(t, ok, "the root path means no prompt")
	require.Error(t, env.registry.register("/not/a/prompt", env.ownerA))
	require.Error(t, env.registry.register(registryTestPrompt, ""))

	require.NoError(t, env.registry.register(registryTestPrompt, env.ownerA))
	require.NoError(t, env.registry.register(registryTestPrompt, env.ownerA), "same-owner registration is idempotent")
	require.Error(t, env.registry.register(registryTestPrompt, env.ownerB), "a live path must not transfer owners")
	lease, ok := env.registry.lookup(registryTestPrompt)
	require.True(t, ok)
	assert.Equal(t, env.ownerA, lease.owner)
}

func TestPromptRegistryPathReuseIsGenerationSafe(t *testing.T) {
	env := newPromptRegistryTestEnv(t)

	require.NoError(t, env.registry.register(registryTestPrompt, env.ownerA))
	old, ok := env.registry.lookup(registryTestPrompt)
	require.True(t, ok)
	env.registry.complete(registryTestPrompt)

	require.NoError(t, env.registry.register(registryTestPrompt, env.ownerB))
	env.registry.unregisterIf(registryTestPrompt, old.token)
	current, ok := env.registry.lookup(registryTestPrompt)
	require.True(t, ok, "cleanup from the old lifetime removed a reused path")
	assert.Equal(t, env.ownerB, current.owner)
}

func TestPromptRegistryRemovesAllPromptsOnDisconnect(t *testing.T) {
	env := newPromptRegistryTestEnv(t)
	pathA := dbus.ObjectPath("/org/freedesktop/secrets/prompt/a")
	pathB := dbus.ObjectPath("/org/freedesktop/secrets/prompt/b")
	require.NoError(t, env.registry.register(pathA, env.ownerA))
	require.NoError(t, env.registry.register(pathB, env.ownerA))
	require.NoError(t, env.registry.register(registryTestPrompt, env.ownerB))

	require.NoError(t, env.clientA.Close())
	require.Eventually(t, func() bool {
		_, hasA := env.registry.lookup(pathA)
		_, hasB := env.registry.lookup(pathB)
		return !hasA && !hasB
	}, 2*time.Second, 10*time.Millisecond)

	lease, ok := env.registry.lookup(registryTestPrompt)
	require.True(t, ok, "disconnecting one sender removed another sender's prompt")
	assert.Equal(t, env.ownerB, lease.owner)
}

func TestPromptRegistryCloseReleasesTrackerLifetimes(t *testing.T) {
	env := newPromptRegistryTestEnv(t)
	require.NoError(t, env.registry.register(registryTestPrompt, env.ownerA))
	env.registry.close()

	_, ok := env.registry.lookup(registryTestPrompt)
	assert.False(t, ok)
	env.tracker.mu.Lock()
	_, tracked := env.tracker.clients[env.ownerA]
	env.tracker.mu.Unlock()
	assert.False(t, tracked)
	require.Error(t, env.registry.register(registryTestPrompt, env.ownerA))
}
