package proxy

import (
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/dhcrypto"
)

// sessionEntry records the upstream session backing a client session, plus the
// negotiated cipher when the client opened an encrypted (DH) session.
type sessionEntry struct {
	local  dbus.ObjectPath   // upstream Secret Service session path
	cipher *dhcrypto.Session // nil for "plain" sessions
}

// SessionManager tracks the mapping between remote (client-facing) sessions and
// the upstream sessions that back them.
//
// The proxy is a man-in-the-middle: it terminates the client's session
// encryption itself (the "plain" pass-through or a real DH key agreement) and
// always opens a "plain" session with the upstream Secret Service. The upstream
// connection is local IPC the proxy already trusts, so encrypting that leg adds
// no security; keeping it plain lets the proxy transcode secrets for clients
// that require DH even when the backend is queried in plain.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[dbus.ObjectPath]sessionEntry // remote -> entry
	counter  atomic.Uint64
}

// NewSessionManager creates a new session manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[dbus.ObjectPath]sessionEntry),
	}
}

// CreateSession negotiates a client session for the given algorithm, opens a
// plain session on the upstream Secret Service, and tracks the mapping. It
// returns the output variant (empty for plain, the service DH public key for
// DH) and the remote session path to give to the client.
func (m *SessionManager) CreateSession(localConn *dbus.Conn, algorithm string, input dbus.Variant) (output dbus.Variant, remotePath dbus.ObjectPath, err error) {
	var clientOutput dbus.Variant
	var cipher *dhcrypto.Session

	switch algorithm {
	case dbustypes.AlgorithmPlain:
		// For "plain" the output is an empty string and no cipher is used.
		clientOutput = dbus.MakeVariant("")
	case dbustypes.AlgorithmDH:
		clientPub, ok := input.Value().([]byte)
		if !ok {
			return dbus.Variant{}, "", dbustypes.NewDBusError(dbustypes.ErrInvalidSignature,
				"dh-ietf1024-sha256-aes128-cbc-pkcs7 OpenSession requires a byte-array public key")
		}
		kp, err := dhcrypto.GenerateKeyPair()
		if err != nil {
			return dbus.Variant{}, "", err
		}
		session, err := kp.Derive(clientPub)
		if err != nil {
			return dbus.Variant{}, "", dbustypes.NewDBusError(dbustypes.ErrNotSupported, err.Error())
		}
		cipher = session
		clientOutput = dbus.MakeVariant(kp.Public)
	default:
		return dbus.Variant{}, "", dbustypes.ErrUnsupportedAlgorithm(algorithm)
	}

	// Always open a "plain" session with the upstream Secret Service, regardless
	// of what the client negotiated (see the SessionManager doc comment).
	obj := localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface+".OpenSession", 0, dbustypes.AlgorithmPlain, dbus.MakeVariant(""))
	if call.Err != nil {
		return dbus.Variant{}, "", call.Err
	}

	var localOutput dbus.Variant
	var localPath dbus.ObjectPath
	if err := call.Store(&localOutput, &localPath); err != nil {
		return dbus.Variant{}, "", err
	}

	// Generate remote session path
	id := m.counter.Add(1)
	remotePath = dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/secrets/session/%d", id))

	m.mu.Lock()
	m.sessions[remotePath] = sessionEntry{local: localPath, cipher: cipher}
	m.mu.Unlock()

	return clientOutput, remotePath, nil
}

// GetLocalSession returns the upstream session path for a remote session.
func (m *SessionManager) GetLocalSession(remotePath dbus.ObjectPath) (dbus.ObjectPath, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.sessions[remotePath]
	return entry.local, ok
}

// ForClient prepares a secret retrieved from upstream for delivery to the client
// over the given remote session: it rewrites the session path and, for DH
// sessions, encrypts the value with a fresh IV placed in Parameters. Plain
// sessions are returned with only the session path rewritten.
func (m *SessionManager) ForClient(remotePath dbus.ObjectPath, secret dbustypes.Secret) (dbustypes.Secret, error) {
	m.mu.RLock()
	cipher := m.sessions[remotePath].cipher
	m.mu.RUnlock()

	secret.Session = remotePath
	if cipher != nil {
		ciphertext, iv, err := cipher.Encrypt(secret.Value)
		if err != nil {
			return dbustypes.Secret{}, err
		}
		secret.Value = ciphertext
		secret.Parameters = iv
	}
	return secret, nil
}

// ForUpstream converts a secret received from the client (on its remote session)
// into the secret to forward to the upstream service: it maps the session to the
// upstream-local path and, for DH sessions, decrypts the value using the IV in
// Parameters. The upstream leg is always plain, so Parameters is cleared for DH.
// ok is false if the remote session is unknown.
func (m *SessionManager) ForUpstream(secret dbustypes.Secret) (out dbustypes.Secret, ok bool, err error) {
	m.mu.RLock()
	entry, found := m.sessions[secret.Session]
	m.mu.RUnlock()
	if !found {
		return dbustypes.Secret{}, false, nil
	}

	out = dbustypes.Secret{
		Session:     entry.local,
		Parameters:  secret.Parameters,
		Value:       secret.Value,
		ContentType: secret.ContentType,
	}
	if entry.cipher != nil {
		plaintext, derr := entry.cipher.Decrypt(secret.Parameters, secret.Value)
		if derr != nil {
			return dbustypes.Secret{}, true, derr
		}
		out.Value = plaintext
		out.Parameters = nil
	}
	return out, true, nil
}

// CloseSession removes a session mapping and closes the local session.
func (m *SessionManager) CloseSession(localConn *dbus.Conn, remotePath dbus.ObjectPath) error {
	m.mu.Lock()
	entry, ok := m.sessions[remotePath]
	if ok {
		delete(m.sessions, remotePath)
	}
	m.mu.Unlock()

	if !ok {
		return dbustypes.ErrSessionNotFound(string(remotePath))
	}

	// Close the local session
	obj := localConn.Object(dbustypes.BusName, entry.local)
	call := obj.Call(dbustypes.SessionInterface+".Close", 0)
	return call.Err
}

// CloseAll closes all sessions.
func (m *SessionManager) CloseAll(localConn *dbus.Conn) {
	m.mu.Lock()
	sessions := make(map[dbus.ObjectPath]sessionEntry, len(m.sessions))
	maps.Copy(sessions, m.sessions)
	m.sessions = make(map[dbus.ObjectPath]sessionEntry)
	m.mu.Unlock()

	for _, entry := range sessions {
		obj := localConn.Object(dbustypes.BusName, entry.local)
		obj.Call(dbustypes.SessionInterface+".Close", 0)
	}
}
