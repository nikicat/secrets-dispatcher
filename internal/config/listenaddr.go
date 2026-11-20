package config

import (
	"fmt"
	"net"
	"net/url"
)

// The host:port on which serve's HTTP API and web UI listen, such as
// DefaultListenAddr.
type ListenAddr string

func (a ListenAddr) String() string { return string(a) }

// Parses a listen address. An empty text is unset (ARCHITECTURE.md, "Command
// line and settings"). Errors when text is not a host:port.
func (a *ListenAddr) UnmarshalText(text []byte) error {
	if len(text) > 0 {
		if _, _, err := net.SplitHostPort(string(text)); err != nil {
			return fmt.Errorf("listen address %q: %w", text, err)
		}
	}
	*a = ListenAddr(text)
	return nil
}

// Returns a, or DefaultListenAddr when a is empty.
func (a ListenAddr) OrDefault() ListenAddr {
	if a == "" {
		return DefaultListenAddr
	}
	return a
}

// Returns the root URL of the API and the web UI, with no trailing slash.
func (a ListenAddr) BaseURL() string { return "http://" + string(a) }

// Returns the web UI link that exchanges the given login token for a browser
// session (ADR 0006).
func (a ListenAddr) LoginURL(token string) string {
	return a.webUIURL(url.Values{"token": {token}})
}

// Returns the web UI link that shows only the pending request with the given id.
func (a ListenAddr) RequestURL(id string) string {
	return a.webUIURL(url.Values{"request": {id}})
}

func (a ListenAddr) webUIURL(query url.Values) string {
	return a.BaseURL() + "/?" + query.Encode()
}
