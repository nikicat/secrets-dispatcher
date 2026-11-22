package proxy

import (
	"errors"
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

// Maps the sessions that clients open to the plain backend sessions behind
// them, terminating each client's session encryption itself (ADR 0001).
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

// Opens a client session of the given algorithm over a new plain backend
// session, and returns the algorithm's output and the client's session path.
// Errors with the *dbus.Error to reply with when the algorithm or its input is
// unusable.
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
			return dbus.Variant{}, "", dbustypes.InvalidSignature.WithMessage(
				"dh-ietf1024-sha256-aes128-cbc-pkcs7 OpenSession requires a byte-array public key")
		}
		kp, err := dhcrypto.GenerateKeyPair()
		if err != nil {
			return dbus.Variant{}, "", err
		}
		session, err := kp.Derive(clientPub)
		if err != nil {
			return dbus.Variant{}, "", dbustypes.NotSupported.WithMessage(err.Error())
		}
		cipher = session
		clientOutput = dbus.MakeVariant(kp.Public)
	default:
		return dbus.Variant{}, "", dbustypes.ErrUnsupportedAlgorithm(algorithm)
	}

	obj := localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface.Member("OpenSession"), 0, dbustypes.AlgorithmPlain, dbus.MakeVariant(""))
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

var errUnknownSession = errors.New("no such client session")

// Returns the backend's secret as the given client session carries it, encrypted
// when that session is DH. Errors with errUnknownSession when no client session
// has the given path.
func (m *SessionManager) ForClient(remotePath dbus.ObjectPath, secret dbustypes.Secret) (dbustypes.Secret, error) {
	m.mu.RLock()
	entry, found := m.sessions[remotePath]
	m.mu.RUnlock()
	if !found {
		return dbustypes.Secret{}, errUnknownSession
	}

	secret.Session = remotePath
	if entry.cipher != nil {
		ciphertext, iv, err := entry.cipher.Encrypt(secret.Value)
		if err != nil {
			return dbustypes.Secret{}, err
		}
		secret.Value = ciphertext
		secret.Parameters = iv
	}
	return secret, nil
}

// Returns the client's secret as the backend session takes it, decrypted when the
// client's session is DH. Errors with errUnknownSession when no client session
// has the secret's session path.
func (m *SessionManager) ForUpstream(secret dbustypes.Secret) (dbustypes.Secret, error) {
	m.mu.RLock()
	entry, found := m.sessions[secret.Session]
	m.mu.RUnlock()
	if !found {
		return dbustypes.Secret{}, errUnknownSession
	}

	out := dbustypes.Secret{
		Session:     entry.local,
		Parameters:  secret.Parameters,
		Value:       secret.Value,
		ContentType: secret.ContentType,
	}
	if entry.cipher != nil {
		plaintext, err := entry.cipher.Decrypt(secret.Parameters, secret.Value)
		if err != nil {
			return dbustypes.Secret{}, err
		}
		out.Value = plaintext
		out.Parameters = nil
	}
	return out, nil
}

// Returns the D-Bus error for a secret that could not be carried across the
// given client session.
func sessionSecretError(session dbus.ObjectPath, err error) *dbus.Error {
	if errors.Is(err, errUnknownSession) {
		return dbustypes.ErrSessionNotFound(string(session))
	}
	return dbustypes.ErrFailed(err)
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
	call := obj.Call(dbustypes.SessionInterface.Member("Close"), 0)
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
		obj.Call(dbustypes.SessionInterface.Member("Close"), 0)
	}
}
