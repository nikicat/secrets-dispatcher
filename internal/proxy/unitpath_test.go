package proxy

import "testing"

func TestDecodeUnitPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "ssh service",
			path: "/org/freedesktop/systemd1/unit/ssh_2eservice",
			want: "ssh.service",
		},
		{
			name: "service with hyphen",
			path: "/org/freedesktop/systemd1/unit/foo_2dbar_2eservice",
			want: "foo-bar.service",
		},
		{
			name: "service with underscore",
			path: "/org/freedesktop/systemd1/unit/my_5fapp_2eservice",
			want: "my_app.service",
		},
		{
			name: "scope unit",
			path: "/org/freedesktop/systemd1/unit/tmux_2dspawn_2d0aafefab_2d9589_2d4202_2db2af_2dd21fe94b5bf3_2escope",
			want: "tmux-spawn-0aafefab-9589-4202-b2af-d21fe94b5bf3.scope",
		},
		{
			name: "no prefix",
			path: "/some/other/path",
			want: "",
		},
		{
			name: "empty path",
			path: "",
			want: "",
		},
		{
			name: "just prefix",
			path: "/org/freedesktop/systemd1/unit/",
			want: "",
		},
		{
			name: "plain unit name",
			path: "/org/freedesktop/systemd1/unit/simple",
			want: "simple",
		},
		{
			name: "uppercase hex",
			path: "/org/freedesktop/systemd1/unit/foo_2Ebar",
			want: "foo.bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecodeUnitPath(tt.path)
			if got != tt.want {
				t.Errorf("DecodeUnitPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestDecodeDBusPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no escapes",
			input: "simple",
			want:  "simple",
		},
		{
			name:  "period",
			input: "foo_2ebar",
			want:  "foo.bar",
		},
		{
			name:  "hyphen",
			input: "foo_2dbar",
			want:  "foo-bar",
		},
		{
			name:  "underscore",
			input: "foo_5fbar",
			want:  "foo_bar",
		},
		{
			name:  "multiple escapes",
			input: "foo_2dbar_2ebaz_5fqux",
			want:  "foo-bar.baz_qux",
		},
		{
			name:  "incomplete escape at end",
			input: "foo_2",
			want:  "foo_2",
		},
		{
			name:  "incomplete escape single char",
			input: "foo_a",
			want:  "foo_a",
		},
		{
			name:  "invalid hex",
			input: "foo_ggbar",
			want:  "foo_ggbar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeDBusPath(tt.input)
			if got != tt.want {
				t.Errorf("decodeDBusPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
