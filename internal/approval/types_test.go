package approval

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientFromSocket(t *testing.T) {
	tests := []struct {
		path string
		want string
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

	for _, path := range []string{"/run/local.sock", "/run/ssh-agent.sock", "/run/unknown", "/run/local", "/run/.sock", "/run/local.sock/"} {
		_, err := ClientFromSocket(path)
		assert.Error(t, err, path)
	}
}
