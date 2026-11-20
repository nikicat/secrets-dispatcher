package systemd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommandExecLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  Command
		want string
	}{
		{"plain words", NewCommand("/usr/bin/x", "serve", "--config", "/home/u/c.yaml"), "/usr/bin/x serve --config /home/u/c.yaml"},
		{"specifiers stay live", NewCommand("/usr/bin/x", "--dir=%t/x"), "/usr/bin/x --dir=%t/x"},
		{"space in a path", NewCommand("/opt/my apps/x", "--config", "/home/u/my config.yaml"), `"/opt/my apps/x" --config "/home/u/my config.yaml"`},
		{"quotes and backslashes", NewCommand("/usr/bin/x", `a"b`, `c\d`, "it's"), `/usr/bin/x "a\"b" "c\\d" "it's"`},
		{"empty and semicolon", NewCommand("/usr/bin/x", "", ";"), `/usr/bin/x "" ";"`},
		{"raw line verbatim", RawCommand(`/opt/b --flag "two words"`), `/opt/b --flag "two words"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.cmd.ExecLine())
		})
	}
}
