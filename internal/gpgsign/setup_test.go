package gpgsign

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWrapperScript(t *testing.T) {
	assert.Equal(t, "#!/bin/sh\nexec '/usr/bin/sd' gpg-sign \"$@\"\n",
		wrapperScript("/usr/bin/sd", ""))
	assert.Equal(t, "#!/bin/sh\nexport "+remoteSocketEnv+"='/run/user/1001/my r.sock'\nexec '/opt/it'\\''s/sd' gpg-sign \"$@\"\n",
		wrapperScript("/opt/it's/sd", "/run/user/1001/my r.sock"))
}
