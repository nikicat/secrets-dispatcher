package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestNewAuthToken(t *testing.T) {
	token, err := NewAuthToken()
	if err != nil {
		t.Fatalf("NewAuthToken: %v", err)
	}
	raw, err := hex.DecodeString(string(token))
	if err != nil {
		t.Fatalf("NewAuthToken() = %q, not hex: %v", string(token), err)
	}
	if len(raw) != 32 {
		t.Errorf("NewAuthToken() holds %d bytes, want 32", len(raw))
	}
}

func TestAuthorizationRoundTrip(t *testing.T) {
	token, err := NewAuthToken()
	if err != nil {
		t.Fatalf("NewAuthToken: %v", err)
	}
	header := http.Header{}
	token.Authorize(header)
	if got, want := header.Get("Authorization"), "Bearer "+string(token); got != want {
		t.Errorf("Authorize set Authorization to %q, want %q", got, want)
	}
	got, err := ParseAuthorization(header)
	if err != nil {
		t.Fatalf("ParseAuthorization(%v): %v", header, err)
	}
	if !got.Equal(token) {
		t.Errorf("ParseAuthorization(%v) = %q, want %q", header, string(got), string(token))
	}
}

func TestParseAuthorization(t *testing.T) {
	tests := []struct {
		value   string
		want    AuthToken
		wantErr error
	}{
		{"", "", ErrNoAuthorization},
		{"abc", "", ErrMalformedAuthorization},
		{"Basic abc", "", ErrMalformedAuthorization},
		{"Bearerabc", "", ErrMalformedAuthorization},
		{"Bearer ", "", nil},
		{"Bearer abc", "abc", nil},
	}
	for _, tc := range tests {
		header := http.Header{}
		if tc.value != "" {
			header.Set("Authorization", tc.value)
		}
		got, err := ParseAuthorization(header)
		if !errors.Is(err, tc.wantErr) || got != tc.want {
			t.Errorf("ParseAuthorization(%q) = %q, %v; want %q, %v",
				tc.value, string(got), err, string(tc.want), tc.wantErr)
		}
	}
}

func TestAuthTokenEqual(t *testing.T) {
	if !AuthToken("abc").Equal("abc") {
		t.Error(`AuthToken("abc").Equal("abc") = false`)
	}
	for _, other := range []AuthToken{"abd", "ab", ""} {
		if AuthToken("abc").Equal(other) {
			t.Errorf("AuthToken(%q).Equal(%q) = true", "abc", string(other))
		}
	}
}

func TestAuthTokenRedacts(t *testing.T) {
	if got := fmt.Sprintf("%v %s", AuthToken("secret"), AuthToken("secret")); got != "[redacted] [redacted]" {
		t.Errorf("formatted token = %q, want it redacted", got)
	}
}
