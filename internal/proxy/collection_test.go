package proxy

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestIsCollectionPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Collection paths
		{"/org/freedesktop/secrets/collection/default", true},
		{"/org/freedesktop/secrets/collection/login", true},

		// Alias paths
		{"/org/freedesktop/secrets/aliases/default", true},
		{"/org/freedesktop/secrets/aliases/login", true},

		// Item paths (not collections)
		{"/org/freedesktop/secrets/collection/default/item1", false},
		{"/org/freedesktop/secrets/aliases/default/item1", false},

		// Invalid paths
		{"/org/freedesktop/secrets/collection/", false},
		{"/org/freedesktop/secrets/aliases/", false},
		{"/org/freedesktop/secrets", false},
		{"/org/freedesktop/secrets/other/foo", false},
		{"/completely/different/path", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := isCollectionPath(dbus.ObjectPath(tc.path))
			if got != tc.want {
				t.Errorf("isCollectionPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
