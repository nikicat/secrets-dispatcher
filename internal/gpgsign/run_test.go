package gpgsign

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsSignRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		// git's real signing invocation (commits, tags, signed pushes).
		{"git sign -bsau", []string{"--status-fd=2", "-bsau", "846FFFEFC1039264"}, true},
		{"separate -s -u", []string{"-s", "-u", "KEY"}, true},
		{"detach-sign short", []string{"-b", "-a", "-u", "KEY"}, true},
		{"long --sign", []string{"--sign"}, true},
		{"long --detach-sign", []string{"--detach-sign"}, true},
		{"long --clearsign", []string{"--clearsign"}, true},
		{"long --clear-sign", []string{"--clear-sign"}, true},

		// git's real verification invocation — must NOT be treated as signing.
		{"git verify", []string{"--keyid-format=long", "--status-fd=1", "--verify", "/tmp/.git_vtag_tmpX", "-"}, false},
		{"list keys", []string{"--list-keys"}, false},
		{"list secret keys short", []string{"-K"}, false},
		{"version probe", []string{"--version"}, false},
		{"fingerprint", []string{"--fingerprint"}, false},
		{"lone dash stdin", []string{"-"}, false},
		{"empty", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isSignRequest(tc.args), "args=%v", tc.args)
		})
	}
}

// TestRunDelegatesVerificationToRealGPG is the regression test for the verify
// pass-through: git verifies a signature by invoking gpg.program with --verify,
// and the proxy must hand that straight to the real gpg — forwarding argv and
// the signed payload on stdin, and propagating gpg's exit code — instead of
// treating it as a signing request. Before the pass-through the proxy tried to
// sign, emitted no GOODSIG, and git reported every validly-signed commit as
// unsigned (git log --show-signature -> "N").
func TestRunDelegatesVerificationToRealGPG(t *testing.T) {
	// git's actual verify argv (captured empirically), pointing at a real
	// (harmless) temp file so nothing depends on its contents.
	verifyArgs := []string{"--keyid-format=long", "--status-fd=1", "--verify", "/tmp/some.sig", "-"}
	payload := "the exact bytes git says were signed\n"

	newFakeGPG := func(t *testing.T, exitCode string) (dir, capture string) {
		t.Helper()
		dir = t.TempDir()
		capture = filepath.Join(dir, "capture")
		// Fake gpg: record argv and stdin to the capture file, then exit with
		// the requested code (mimicking gpg's 0=good / 1=bad-signature).
		script := "#!/bin/sh\n" +
			"{ printf 'ARGS: %s\\n' \"$*\"; printf -- '---STDIN---\\n'; cat; } > '" + capture + "'\n" +
			"exit " + exitCode + "\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gpg"), []byte(script), 0o755))
		return dir, capture
	}

	isolate := func(t *testing.T, gpgDir string) {
		t.Helper()
		// Prepend the fake gpg so FindRealGPG picks it up first (but keep the
		// real PATH so the fake's shell can still find cat/sh). The daemon's
		// cookie/socket point at nonexistent paths, so if Run ever took the
		// signing path instead of delegating it would fail loudly, not pass by
		// luck.
		t.Setenv("PATH", gpgDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("XDG_STATE_HOME", filepath.Join(gpgDir, "no-state"))
		t.Setenv("XDG_RUNTIME_DIR", filepath.Join(gpgDir, "no-run"))
	}

	t.Run("forwards argv and stdin, good signature -> exit 0", func(t *testing.T) {
		dir, capture := newFakeGPG(t, "0")
		isolate(t, dir)

		exit := Run(verifyArgs, strings.NewReader(payload))
		require.Equal(t, 0, exit)

		got, err := os.ReadFile(capture)
		require.NoError(t, err, "real gpg was never called — Run did not delegate verification")
		out := string(got)
		assert.Contains(t, out, "--verify")
		assert.Contains(t, out, "/tmp/some.sig", "signature-file argument was not forwarded")
		assert.Contains(t, out, payload, "signed payload on stdin was not forwarded to gpg")
	})

	t.Run("propagates gpg's non-zero exit (bad signature)", func(t *testing.T) {
		dir, _ := newFakeGPG(t, "1")
		isolate(t, dir)

		exit := Run(verifyArgs, strings.NewReader(payload))
		assert.Equal(t, 1, exit, "gpg's failing exit code must propagate so git reports a bad signature")
	})
}
