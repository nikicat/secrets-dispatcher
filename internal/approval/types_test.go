package approval

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

func TestClientFromSocket(t *testing.T) {
	tests := []struct {
		path unixsock.Path
		want ClientName
	}{
		{"/path/to/myserver.sock", "myserver"},
		{"relative/path.sock", "path"},
		{"/run/myserver", "myserver"},
	}
	for _, tc := range tests {
		got, err := ClientFromSocket(tc.path)
		assert.NoError(t, err, tc.path)
		assert.Equal(t, tc.want, got, tc.path)
	}

	for _, path := range []unixsock.Path{"/run/local.sock", "/run/ssh-agent.sock", "/run/unknown", "/run/local", "/run/.sock", "/run/local.sock/"} {
		_, err := ClientFromSocket(path)
		assert.Error(t, err, path)
	}
}
