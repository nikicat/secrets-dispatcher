package gpgsign

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseGPGArgs(t *testing.T) {
	passThrough := func(args ...string) gpgCall { return passThroughCall{args: args} }
	sign := func(key approval.GPGKeyID) gpgCall { return signCall{keyID: key} }
	tests := []struct {
		name string
		args []string
		want gpgCall
	}{
		// git's real signing invocation (commits, tags, signed pushes).
		{"git sign -bsau", []string{"--status-fd=2", "-bsau", "846FFFEFC1039264"}, sign("846FFFEFC1039264")},
		{"separate -bsa -u", []string{"--status-fd=2", "-bsa", "-u", "846FFFEFC1039264"}, sign("846FFFEFC1039264")},
		{"separate -s -u", []string{"-s", "-u", "KEY"}, sign("KEY")},
		{"detach-sign short", []string{"-b", "-a", "-u", "KEY"}, sign("KEY")},
		{"long --sign", []string{"--sign", "-u", "KEY"}, sign("KEY")},
		{"long --detach-sign", []string{"--detach-sign", "-u", "KEY"}, sign("KEY")},
		{"long --clearsign", []string{"--clearsign", "-u", "KEY"}, sign("KEY")},
		{"long --clear-sign", []string{"--clear-sign", "-u", "KEY"}, sign("KEY")},

		// git's real verification invocation — must NOT be treated as signing.
		{"git verify", []string{"--keyid-format=long", "--status-fd=1", "--verify", "/tmp/.git_vtag_tmpX", "-"},
			passThrough("--keyid-format=long", "--status-fd=1", "--verify", "/tmp/.git_vtag_tmpX", "-")},
		{"list keys", []string{"--list-keys"}, passThrough("--list-keys")},
		{"list secret keys short", []string{"-K"}, passThrough("-K")},
		{"version probe", []string{"--version"}, passThrough("--version")},
		{"fingerprint", []string{"--fingerprint"}, passThrough("--fingerprint")},
		{"lone dash stdin", []string{"-"}, passThrough("-")},
		{"only -u", []string{"-u", "ABCD1234"}, passThrough("-u", "ABCD1234")},
		{"empty", nil, passThroughCall{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGPGArgs(tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got, "args=%v", tc.args)
		})
	}
}

func TestParseGPGArgsRefusesSigningWithoutKey(t *testing.T) {
	for _, args := range [][]string{{"--status-fd=2", "-bsa"}, {"--sign"}, {"-s", "-u"}} {
		_, err := parseGPGArgs(args)
		assert.ErrorIs(t, err, errNoSigningKey, "args=%v", args)
	}
}

// Checks that git's verify call reaches the real gpg with its arguments and
// stdin, and that gpg's exit code comes back. A proxy that signs instead prints
// no GOODSIG, so git reports every validly signed commit as unsigned.
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
		// real PATH so the fake's shell can still find cat/sh). The config,
		// cookie and socket point at nonexistent paths, so if Run ever took the
		// signing path instead of delegating it would fail loudly, not pass by
		// luck.
		t.Setenv("PATH", gpgDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(gpgDir, "no-config"))
		t.Setenv("XDG_STATE_HOME", filepath.Join(gpgDir, "no-state"))
		t.Setenv("XDG_RUNTIME_DIR", filepath.Join(gpgDir, "no-run"))
	}

	t.Run("forwards argv and stdin, good signature -> exit 0", func(t *testing.T) {
		dir, capture := newFakeGPG(t, "0")
		isolate(t, dir)

		exit := Run(verifyArgs, strings.NewReader(payload))
		require.Equal(t, exitOK, exit)

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
		assert.Equal(t, ExitStatus(1), exit, "gpg's failing exit code must propagate so git reports a bad signature")
	})
}

func TestDaemonStateDirFromConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "/home/u/.state")
	source, err := config.DefaultSource()
	require.NoError(t, err)
	require.NoError(t, source.File.Save(&config.Config{StateDir: "/srv/state"}))

	dir, err := daemonStateDir()
	require.NoError(t, err)
	assert.Equal(t, config.StateDir("/srv/state"), dir)
}

func TestDaemonStateDirWithoutConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "/home/u/.state")

	dir, err := daemonStateDir()
	require.NoError(t, err)
	assert.Equal(t, config.StateDir("/home/u/.state/secrets-dispatcher"), dir)
}

func TestSignOutcomeReport(t *testing.T) {
	tests := []struct {
		name    string
		outcome SignOutcome
		want    ExitStatus
	}{
		{"approved", SignOutcome{Result: approval.ResolutionApproved, GPGResult: &approval.GPGResult{Signature: []byte("SIG")}}, exitOK},
		{"auto-approved", SignOutcome{Result: approval.ResolutionAutoApproved, GPGResult: &approval.GPGResult{}}, exitOK},
		{"approved, gpg failed", SignOutcome{Result: approval.ResolutionApproved, GPGResult: &approval.GPGResult{ExitCode: 3}}, 3},
		{"denied", SignOutcome{Result: approval.ResolutionDenied}, exitRefused},
		{"cancelled", SignOutcome{Result: approval.ResolutionCancelled}, exitRefused},
		{"expired", SignOutcome{Result: approval.ResolutionExpired}, exitRefused},
		{"unknown", SignOutcome{Result: "bogus"}, exitSystemError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.outcome.report())
		})
	}
}

func TestReportFailure(t *testing.T) {
	err := errors.New("daemon unreachable")
	assert.Equal(t, exitSystemError, reportFailure(context.Background(), err))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, exitRefused, reportFailure(ctx, err))
}

// A commit signed outside a work tree fails, rather than reaching a trusted
// signer with a repository name and staged files that git never reported.
func TestCollectSignInfoOutsideWorkTreeFails(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	commit := "tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98\nauthor A <a@x> 1 +0000\ncommitter A <a@x> 1 +0000\n\nfix\n"
	_, err := collectSignInfo("KEY", strings.NewReader(commit))
	assert.ErrorContains(t, err, "find the repository")
}
