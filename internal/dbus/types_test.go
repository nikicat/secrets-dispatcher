package dbus

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestCollectionOf(t *testing.T) {
	type parsed struct {
		ref CollectionRef
		ok  bool
	}
	tests := []struct {
		path dbus.ObjectPath
		want parsed
	}{
		{"/org/freedesktop/secrets/collection/default/i123", parsed{CollectionRef{Name: "default"}, true}},
		{"/org/freedesktop/secrets/collection/login/abc", parsed{CollectionRef{Name: "login"}, true}},
		{"/org/freedesktop/secrets/collection/mykeys", parsed{CollectionRef{Name: "mykeys"}, true}},
		{"/org/freedesktop/secrets/aliases/default", parsed{CollectionRef{Name: "default", Alias: true}, true}},
		{"/org/freedesktop/secrets/session/s1", parsed{}},
		{"", parsed{}},
	}
	for _, tt := range tests {
		ref, ok := CollectionOf(tt.path)
		if got := (parsed{ref, ok}); got != tt.want {
			t.Errorf("CollectionOf(%q) = %+v, want %+v", tt.path, got, tt.want)
		}
	}
}

func TestParseCollectionAndItemPath(t *testing.T) {
	type parsed struct {
		ref CollectionRef
		ok  bool
	}
	tests := []struct {
		path             string
		collection, item parsed
	}{
		{"/org/freedesktop/secrets/collection/login", parsed{CollectionRef{Name: "login"}, true}, parsed{}},
		{"/org/freedesktop/secrets/aliases/default", parsed{CollectionRef{Name: "default", Alias: true}, true}, parsed{}},
		{"/org/freedesktop/secrets/collection/login/i1", parsed{}, parsed{CollectionRef{Name: "login"}, true}},
		{"/org/freedesktop/secrets/aliases/default/i1", parsed{}, parsed{CollectionRef{Name: "default", Alias: true}, true}},
		{"/org/freedesktop/secrets/collection/login/", parsed{}, parsed{}},
		{"/org/freedesktop/secrets/collection//i1", parsed{}, parsed{}},
		{"/org/freedesktop/secrets/collection/login/i1/x", parsed{}, parsed{}},
		{"/org/freedesktop/secrets/collection/", parsed{}, parsed{}},
		{"/org/freedesktop/secrets/aliases/", parsed{}, parsed{}},
		{"/org/freedesktop/secrets", parsed{}, parsed{}},
		{"/org/freedesktop/secrets/session/s1", parsed{}, parsed{}},
	}
	for _, tt := range tests {
		path := dbus.ObjectPath(tt.path)
		ref, ok := ParseCollectionPath(path)
		if got := (parsed{ref, ok}); got != tt.collection {
			t.Errorf("ParseCollectionPath(%q) = %+v, want %+v", tt.path, got, tt.collection)
		}
		ref, ok = ParseItemPath(path)
		if got := (parsed{ref, ok}); got != tt.item {
			t.Errorf("ParseItemPath(%q) = %+v, want %+v", tt.path, got, tt.item)
		}
	}
}
