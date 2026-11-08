package config

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/randhex"
)

// The API's master token. Same-user thin clients send it as their Bearer
// credential. The daemon keeps it in the state directory.
type AuthToken string

// The number of random bytes in a new token, hex-encoded in the cookie file.
const authTokenSize = 32

// The HTTP header that carries the token, and its scheme.
const (
	authorizationHeader = "Authorization"
	bearerScheme        = "Bearer"
)

var (
	// The request has no Authorization header.
	ErrNoAuthorization = errors.New("no Authorization header")
	// The Authorization header is not in the "Bearer <token>" form.
	ErrMalformedAuthorization = errors.New("invalid Authorization header format")
)

// Returns a new random token. Errors when the system's random source fails.
func NewAuthToken() (AuthToken, error) {
	s, err := randhex.String(authTokenSize)
	if err != nil {
		return "", err
	}
	return AuthToken(s), nil
}

// Parses the token from h's Authorization header, of the form
// "Bearer <token>". Errors with ErrNoAuthorization when h has no such header,
// and with ErrMalformedAuthorization when it has another form.
func ParseAuthorization(h http.Header) (AuthToken, error) {
	value := h.Get(authorizationHeader)
	if value == "" {
		return "", ErrNoAuthorization
	}
	scheme, token, ok := strings.Cut(value, " ")
	if !ok || scheme != bearerScheme {
		return "", ErrMalformedAuthorization
	}
	return AuthToken(token), nil
}

// Sets h's Authorization header to carry t, as ParseAuthorization reads it.
func (t AuthToken) Authorize(h http.Header) {
	h.Set(authorizationHeader, bearerScheme+" "+string(t))
}

// Reports whether t and other are the same token, in time that does not depend
// on where they differ.
func (t AuthToken) Equal(other AuthToken) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(other)) == 1
}

// Returns a placeholder, so that a formatted or logged token does not leak.
func (t AuthToken) String() string { return "[redacted]" }
