package service

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// colorize reports whether the milestone/trial output printed by this package
// carries ANSI styling. It is enabled only when stdout is a real terminal,
// NO_COLOR is unset (https://no-color.org), and TERM is not "dumb". Piped or
// redirected output — CI capture, `nohup … >file`, `cmd | …`, the Tier-2 e2e
// harness — stays plain, so greps and byte-exact snapshots are unaffected.
//
// It is a package var (not a const) so tests can force styling on to assert the
// exact escapes without needing a pty.
var colorize = detectStdoutColor()

func detectStdoutColor() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	// A terminal answers TCGETS; a pipe/file/socket returns ENOTTY.
	_, err := unix.IoctlGetTermios(int(os.Stdout.Fd()), unix.TCGETS)
	return err == nil
}

// SGR (Select Graphic Rendition) parameters. Combined with ';' by paint.
const (
	sgrReset  = "\x1b[0m"
	sgrBold   = "1"
	sgrFaint  = "2"
	sgrRed    = "31"
	sgrGreen  = "32"
	sgrYellow = "33"
	sgrCyan   = "36"
)

// paint wraps s in the given SGR codes when color is enabled, else returns s
// unchanged. Callers pad/format the plain text first (widths must not count the
// escape bytes), then hand the finished string here.
func paint(s string, codes ...string) string {
	if !colorize || len(codes) == 0 {
		return s
	}
	return "\x1b[" + strings.Join(codes, ";") + "m" + s + sgrReset
}

// Semantic wrappers keep call sites reading intent, not raw codes.
func cOK(s string) string   { return paint(s, sgrGreen) }  // ✓ success milestones
func cWarn(s string) string { return paint(s, sgrYellow) } // WARNING / recoverable notes
func cErr(s string) string  { return paint(s, sgrRed) }    // failures being rolled back
func cInfo(s string) string { return paint(s, sgrCyan) }   // → next-step arrows, change verbs
func cDim(s string) string  { return paint(s, sgrFaint) }  // secondary/explanatory text
func cBold(s string) string { return paint(s, sgrBold) }   // things to type, URLs, keys
