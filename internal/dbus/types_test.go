package dbus

import "testing"

func TestExtractCollection(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/org/freedesktop/secrets/collection/default/i123", "default"},
		{"/org/freedesktop/secrets/collection/login/abc", "login"},
		{"/org/freedesktop/secrets/collection/mykeys", "mykeys"},
		{"/org/freedesktop/secrets/aliases/default", "default"},
		{"", ""},
	}
	for _, tt := range tests {
		got := ExtractCollection(tt.path)
		if got != tt.want {
			t.Errorf("ExtractCollection(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
