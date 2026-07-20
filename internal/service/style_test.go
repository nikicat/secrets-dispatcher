package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// forceColor pins the package colorize flag for one test and restores it after,
// so styling can be asserted without a pty.
func forceColor(t *testing.T, on bool) {
	t.Helper()
	orig := colorize
	colorize = on
	t.Cleanup(func() { colorize = orig })
}

func TestPaintDisabledReturnsPlain(t *testing.T) {
	forceColor(t, false)
	assert.Equal(t, "hello", paint("hello", sgrGreen))
	assert.Equal(t, "hello", cOK("hello"))
	assert.Equal(t, "hello", cWarn("hello"))
	assert.Equal(t, "hello", cErr("hello"))
	assert.Equal(t, "hello", cInfo("hello"))
	assert.Equal(t, "hello", cDim("hello"))
	assert.Equal(t, "hello", cBold("hello"))
}

func TestPaintEnabledWrapsInSGR(t *testing.T) {
	forceColor(t, true)
	assert.Equal(t, "\x1b[32mok\x1b[0m", cOK("ok"))
	assert.Equal(t, "\x1b[33mwarn\x1b[0m", cWarn("warn"))
	assert.Equal(t, "\x1b[31merr\x1b[0m", cErr("err"))
	assert.Equal(t, "\x1b[36minfo\x1b[0m", cInfo("info"))
	assert.Equal(t, "\x1b[2mdim\x1b[0m", cDim("dim"))
	assert.Equal(t, "\x1b[1mbold\x1b[0m", cBold("bold"))
	// Multiple codes join with ';'.
	assert.Equal(t, "\x1b[1;31mx\x1b[0m", paint("x", sgrBold, sgrRed))
	// No codes is a no-op even when enabled.
	assert.Equal(t, "x", paint("x"))
}

// Change.String must stay byte-identical to the old plain format when color is
// off — the Tier-2 e2e harness greps this output over a non-tty pipe.
func TestChangeStringPlainWhenDisabled(t *testing.T) {
	forceColor(t, false)
	assert.Equal(t, "write   /etc/foo  # the note",
		Change{Op: "write", Target: "/etc/foo", Note: "the note"}.String())
	assert.Equal(t, "remove  /etc/bar",
		Change{Op: "remove", Target: "/etc/bar"}.String())
}

// When enabled, only the verb and the note are wrapped; the target (which the
// e2e grep would key on) stays plain, and column alignment is preserved because
// the verb is padded before it is colorized.
func TestChangeStringColorizedWhenEnabled(t *testing.T) {
	forceColor(t, true)
	assert.Equal(t, "\x1b[36mwrite  \x1b[0m /etc/foo\x1b[2m  # the note\x1b[0m",
		Change{Op: "write", Target: "/etc/foo", Note: "the note"}.String())
}

func TestDetectStdoutColorHonorsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	assert.False(t, detectStdoutColor(), "NO_COLOR must disable color")
}

func TestDetectStdoutColorHonorsDumbTerm(t *testing.T) {
	t.Setenv("TERM", "dumb")
	assert.False(t, detectStdoutColor(), "TERM=dumb must disable color")
}
