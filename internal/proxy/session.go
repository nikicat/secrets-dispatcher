package proxy

import (
	"context"
	"errors"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/dhcrypto"
)

// The upstream session behind a client session, and how that client session
// carries secrets.
type sessionEntry struct {
	upstream dbus.ObjectPath
	codec    sessionCodec
}

// How a client session carries the secrets that the plain upstream session
// holds.
type sessionCodec interface {
	// Returns the given upstream secret as the client session carries it.
	forClient(secret dbustypes.Secret) (dbustypes.Secret, error)
	// Returns the given client secret as the upstream session takes it.
	forUpstream(secret dbustypes.Secret) (dbustypes.Secret, error)
}

// A "plain" client session, which carries secrets unchanged.
type plainSession struct{}

func (plainSession) forClient(secret dbustypes.Secret) (dbustypes.Secret, error) {
	return secret, nil
}

func (plainSession) forUpstream(secret dbustypes.Secret) (dbustypes.Secret, error) {
	return secret, nil
}

// An encrypted (DH) client session, which carries secrets under the cipher it
// negotiated.
type dhSession struct {
	cipher *dhcrypto.Session
}

func (s dhSession) forClient(secret dbustypes.Secret) (dbustypes.Secret, error) {
	ciphertext, iv, err := s.cipher.Encrypt(secret.Value)
	if err != nil {
		return dbustypes.Secret{}, err
	}
	secret.Value = ciphertext
	secret.Parameters = iv
	return secret, nil
}

func (s dhSession) forUpstream(secret dbustypes.Secret) (dbustypes.Secret, error) {
	plaintext, err := s.cipher.Decrypt(secret.Parameters, secret.Value)
	if err != nil {
		return dbustypes.Secret{}, err
	}
	secret.Value = plaintext
	secret.Parameters = nil
	return secret, nil
}

// Maps the sessions that clients open to the plain backend sessions behind
// them, terminating each client's session encryption itself (ADR 0001).
type SessionManager struct {
	backendConn *dbus.Conn
	mu          sync.RWMutex
	sessions    map[dbus.ObjectPath]sessionEntry
	counter     atomic.Uint64
}

func NewSessionManager(backendConn *dbus.Conn) *SessionManager {
	return &SessionManager{
		backendConn: backendConn,
		sessions:    make(map[dbus.ObjectPath]sessionEntry),
	}
}

// Opens a client session of the given algorithm over a new plain backend
// session, and returns the algorithm's output and the client's session path.
// Errors with the *dbus.Error to reply with when the algorithm or its input is
// unusable.
func (m *SessionManager) CreateSession(algorithm string, input dbus.Variant) (output dbus.Variant, clientPath dbus.ObjectPath, err error) {
	var codec sessionCodec

	switch algorithm {
	case dbustypes.AlgorithmPlain:
		codec = plainSession{}
		output = dbus.MakeVariant("")
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
		cipher, err := kp.Derive(clientPub)
		if err != nil {
			return dbus.Variant{}, "", dbustypes.NotSupported.WithMessage(err.Error())
		}
		codec = dhSession{cipher: cipher}
		output = dbus.MakeVariant(kp.Public)
	default:
		return dbus.Variant{}, "", dbustypes.ErrUnsupportedAlgorithm(algorithm)
	}

	obj := m.backendConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface.Member("OpenSession"), 0, dbustypes.AlgorithmPlain, dbus.MakeVariant(""))
	if call.Err != nil {
		return dbus.Variant{}, "", call.Err
	}

	var upstreamOutput dbus.Variant
	var upstreamPath dbus.ObjectPath
	if err := call.Store(&upstreamOutput, &upstreamPath); err != nil {
		return dbus.Variant{}, "", err
	}

	id := m.counter.Add(1)
	clientPath = dbustypes.SessionPath(id)

	m.mu.Lock()
	m.sessions[clientPath] = sessionEntry{upstream: upstreamPath, codec: codec}
	m.mu.Unlock()

	return output, clientPath, nil
}

// Returns the upstream session behind the given client session.
func (m *SessionManager) UpstreamSession(clientPath dbus.ObjectPath) (dbus.ObjectPath, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.sessions[clientPath]
	return entry.upstream, ok
}

var errUnknownSession = errors.New("no such client session")

// Returns the backend's secret as the given client session carries it, encrypted
// when that session is DH. Errors with errUnknownSession when no client session
// has the given path.
func (m *SessionManager) ForClient(clientPath dbus.ObjectPath, secret dbustypes.Secret) (dbustypes.Secret, error) {
	m.mu.RLock()
	entry, found := m.sessions[clientPath]
	m.mu.RUnlock()
	if !found {
		return dbustypes.Secret{}, errUnknownSession
	}

	secret.Session = clientPath
	return entry.codec.forClient(secret)
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

	secret.Session = entry.upstream
	return entry.codec.forUpstream(secret)
}

// Returns the D-Bus error for a secret that could not be carried across the
// given client session.
func sessionSecretError(session dbus.ObjectPath, err error) *dbus.Error {
	if errors.Is(err, errUnknownSession) {
		return dbustypes.ErrSessionNotFound(string(session))
	}
	return dbustypes.ErrFailed(err)
}

// Forgets the given client session and closes the upstream session behind it.
func (m *SessionManager) CloseSession(clientPath dbus.ObjectPath) error {
	m.mu.Lock()
	entry, ok := m.sessions[clientPath]
	if ok {
		delete(m.sessions, clientPath)
	}
	m.mu.Unlock()

	if !ok {
		return dbustypes.ErrSessionNotFound(string(clientPath))
	}

	obj := m.backendConn.Object(dbustypes.BusName, entry.upstream)
	call := obj.Call(dbustypes.SessionInterface.Member("Close"), 0)
	return call.Err
}

// Forgets every client session and closes the upstream sessions behind them,
// giving up on calls that outlast the given context.
func (m *SessionManager) CloseAll(ctx context.Context) {
	m.mu.Lock()
	sessions := make(map[dbus.ObjectPath]sessionEntry, len(m.sessions))
	maps.Copy(sessions, m.sessions)
	m.sessions = make(map[dbus.ObjectPath]sessionEntry)
	m.mu.Unlock()

	for _, entry := range sessions {
		obj := m.backendConn.Object(dbustypes.BusName, entry.upstream)
		obj.CallWithContext(ctx, dbustypes.SessionInterface.Member("Close"), 0)
	}
}

// Serves Close on the client sessions that its manager opened.
type sessionHandler struct {
	sessions *SessionManager
}

func (h sessionHandler) Close(msg dbus.Message) *dbus.Error {
	if err := h.sessions.CloseSession(dbustypes.PathOf(msg)); err != nil {
		return dbustypes.AsDBusError(err)
	}
	return nil
}
