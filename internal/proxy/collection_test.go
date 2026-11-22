package proxy

import (
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

func TestCollectionObjectsServes(t *testing.T) {
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
			got := collectionObjects{}.serves(dbus.ObjectPath(tc.path))
			if got != tc.want {
				t.Errorf("collectionObjects.serves(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestItemObjectsServes(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/org/freedesktop/secrets/collection/default/item1", true},

		{"/org/freedesktop/secrets/collection/default/", false},
		{"/org/freedesktop/secrets/collection//item1", false},
		{"/org/freedesktop/secrets/collection/default/item1/x", false},
		{"/org/freedesktop/secrets/aliases/default/item1", false},
		{"/org/freedesktop/secrets/collection/default", false},
		{"/org/freedesktop/secrets/collection/", false},
		{"/org/freedesktop/secrets", false},
		{"/completely/different/path", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := itemObjects{}.serves(dbus.ObjectPath(tc.path))
			if got != tc.want {
				t.Errorf("itemObjects.serves(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestSearchRequest(t *testing.T) {
	attributes := map[string]string{"service": "gh", "account": "me", "a": ""}
	got := searchRequest(attributes)
	want := approval.RequestInfo{
		Items:   []approval.ItemInfo{{Label: "a=, account=me, service=gh"}},
		Type:    approval.RequestTypeSearch,
		Payload: approval.SearchPayload{Attributes: attributes},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("searchRequest = %+v, want %+v", got, want)
	}
	if got := searchRequest(nil); !reflect.DeepEqual(got.Items, []approval.ItemInfo{{}}) {
		t.Errorf("searchRequest(nil).Items = %+v, want one unlabelled item", got.Items)
	}
}

func TestItemInfoFromProperties(t *testing.T) {
	got := itemInfoFromProperties("/org/freedesktop/secrets/collection/login", map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant("token"),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(map[string]string{"service": "gh"}),
		"Label":                                  dbus.MakeVariant("unqualified"),
	})
	want := approval.ItemInfo{
		Path:       "/org/freedesktop/secrets/collection/login",
		Label:      "token",
		Attributes: map[string]string{"service": "gh"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("itemInfoFromProperties = %+v, want %+v", got, want)
	}

	got = itemInfoFromProperties("/c", map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label": dbus.MakeVariant(42),
	})
	if !reflect.DeepEqual(got, approval.ItemInfo{Path: "/c"}) {
		t.Errorf("itemInfoFromProperties with a mistyped label = %+v, want only the path", got)
	}
}
