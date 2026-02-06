package proxy

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

// SessionManager tracks the mapping between remote sessions and local sessions.
// For MVP, we only support the "plain" algorithm.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[dbus.ObjectPath]dbus.ObjectPath // remote -> local
	counter  atomic.Uint64
}

// NewSessionManager creates a new session manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[dbus.ObjectPath]dbus.ObjectPath),
	}
}

// CreateSession opens a session on the local Secret Service and tracks the mapping.
// Returns the remote session path to give to the client.
func (m *SessionManager) CreateSession(localConn *dbus.Conn, algorithm string, input dbus.Variant) (output dbus.Variant, remotePath dbus.ObjectPath, err error) {
	if algorithm != dbustypes.AlgorithmPlain {
		return dbus.Variant{}, "", dbustypes.ErrUnsupportedAlgorithm(algorithm)
	}

	// Call OpenSession on local Secret Service
	obj := localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface+".OpenSession", 0, algorithm, input)
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
	m.sessions[remotePath] = localPath
	m.mu.Unlock()

	return localOutput, remotePath, nil
}

// GetLocalSession returns the local session path for a remote session.
func (m *SessionManager) GetLocalSession(remotePath dbus.ObjectPath) (dbus.ObjectPath, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	local, ok := m.sessions[remotePath]
	return local, ok
}

// CloseSession removes a session mapping and closes the local session.
func (m *SessionManager) CloseSession(localConn *dbus.Conn, remotePath dbus.ObjectPath) error {
	m.mu.Lock()
	localPath, ok := m.sessions[remotePath]
	if ok {
		delete(m.sessions, remotePath)
	}
	m.mu.Unlock()

	if !ok {
		return dbustypes.ErrSessionNotFound(string(remotePath))
	}

	// Close the local session
	obj := localConn.Object(dbustypes.BusName, localPath)
	call := obj.Call(dbustypes.SessionInterface+".Close", 0)
	return call.Err
}

// CloseAll closes all sessions.
func (m *SessionManager) CloseAll(localConn *dbus.Conn) {
	m.mu.Lock()
	sessions := make(map[dbus.ObjectPath]dbus.ObjectPath, len(m.sessions))
	for k, v := range m.sessions {
		sessions[k] = v
	}
	m.sessions = make(map[dbus.ObjectPath]dbus.ObjectPath)
	m.mu.Unlock()

	for _, localPath := range sessions {
		obj := localConn.Object(dbustypes.BusName, localPath)
		obj.Call(dbustypes.SessionInterface+".Close", 0)
	}
}
