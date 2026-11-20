package sender

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
)

// The web UI and the CLI read every field by these names.
func TestInfoWireFormat(t *testing.T) {
	exe, cwd := procutil.Path("/usr/bin/gh"), procutil.Path("/home/nb")
	info := Info{
		Sender:      new(UniqueName(":1.42")),
		PID:         4242,
		UID:         new(procutil.UID(1000)),
		InvokerName: "gh",
		SystemdUnit: "app-gh.scope",
		ProcessChain: []procutil.Process{
			{Name: "gh", PID: 4242, Exe: &exe, Args: []string{"gh", "auth"}, CWD: &cwd},
		},
	}
	data, err := json.Marshal(info)
	require.NoError(t, err)
	assert.JSONEq(t, `{"sender":":1.42","pid":4242,"uid":1000,"invoker_name":"gh",
		"systemd_unit":"app-gh.scope",
		"process_chain":[{"name":"gh","pid":4242,"exe":"/usr/bin/gh","args":["gh","auth"],"cwd":"/home/nb"}]}`, string(data))

	var back Info
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, info, back)
}

// An unknown uid is left out rather than reading as root, and a request that
// did not come over D-Bus has no sender.
func TestInfoUnknownUIDAndSender(t *testing.T) {
	data, err := json.Marshal(Info{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"pid":0,"invoker_name":""}`, string(data))
}

func TestInfoDisplayName(t *testing.T) {
	assert.Equal(t, "gh", Info{InvokerName: "gh", SystemdUnit: "app-gh.scope"}.DisplayName())
	assert.Equal(t, "app-gh.scope", Info{SystemdUnit: "app-gh.scope"}.DisplayName())
	assert.Empty(t, Info{}.DisplayName())
}

func TestChainReaderLeavesOutSelfExe(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	selfExe := procutil.Path(exe)
	self := procutil.PID(os.Getpid())

	chain := ChainReader{}.Chain(self)
	require.NotEmpty(t, chain)
	assert.Equal(t, self, chain[0].PID)
	assert.Equal(t, &selfExe, chain[0].Exe)

	reader, err := NewChainReader(procutil.ChainToInit)
	require.NoError(t, err)
	assert.True(t, reader.IsSelf(selfExe))
	for _, p := range reader.Chain(self) {
		assert.NotEqual(t, &selfExe, p.Exe)
	}
}
