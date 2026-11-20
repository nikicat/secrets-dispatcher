package systemd

import "strings"

// A command line that a unit runs, as ExecStart= takes it: an argv from
// NewCommand, or a line in systemd's own syntax from RawCommand.
type Command interface {
	// Returns the command as the value of ExecStart=.
	ExecLine() string
	command()
}

// A command given as its words, exe first.
type argv []string

// A command line that the user wrote in systemd's syntax, kept verbatim.
type rawLine string

// Returns the command that runs exe with args.
func NewCommand(exe string, args ...string) Command {
	return argv(append([]string{exe}, args...))
}

// Returns the command written as line, in systemd's own syntax.
func RawCommand(line string) Command { return rawLine(line) }

func (argv) command()    {}
func (rawLine) command() {}

// Returns the words as one ExecStart= line. % specifiers and $ variables stay
// live, since words such as %t/... rely on them.
func (a argv) ExecLine() string {
	words := make([]string, len(a))
	for i, w := range a {
		words[i] = quoteWord(w)
	}
	return strings.Join(words, " ")
}

func (r rawLine) ExecLine() string { return string(r) }

var wordEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)

// Returns w as one ExecStart= word, double-quoted when it is empty or holds
// whitespace, a quote, a backslash or a semicolon (systemd.syntax(7)).
func quoteWord(w string) string {
	if w != "" && !strings.ContainsAny(w, " \t\n\"'\\;") {
		return w
	}
	return `"` + wordEscaper.Replace(w) + `"`
}
