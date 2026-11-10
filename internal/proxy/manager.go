package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// ClientInfo represents information about a connected client.
type ClientInfo struct {
	Name       string `json:"name"`
	SocketPath string `json:"socket_path"`
}

// ClientObserver receives notifications about client connections.
type ClientObserver interface {
	OnClientConnected(client ClientInfo)
	OnClientDisconnected(client ClientInfo)
}

// proxyInstance holds a running proxy and its cancellation function.
type proxyInstance struct {
	proxy  *Proxy
	cancel context.CancelFunc
	done   chan struct{} // closed once the proxy has closed and its name is free
}

// The running proxies by socket path. Only the manager's event loop adds one, and a proxy removes itself
// once it has stopped, so nothing else holds this lock.
type proxySet struct {
	mu     sync.RWMutex
	byPath map[string]*proxyInstance
}

func (s *proxySet) get(path string) (*proxyInstance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.byPath[path]
	return inst, ok
}

func (s *proxySet) add(path string, inst *proxyInstance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byPath[path] = inst
}

func (s *proxySet) remove(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byPath, path)
}

// Returns a copy that later changes do not affect.
func (s *proxySet) snapshot() map[string]*proxyInstance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.byPath)
}

// Manager watches a directory for socket files and manages proxy connections.
type Manager struct {
	socketsDir            string
	upstreamAddr          string // D-Bus address for upstream (empty = session bus)
	proxies               proxySet
	watcher               *fsnotify.Watcher
	approval              *approval.Manager
	logLevel              slog.Level
	upstreamNotifier      UpstreamNotifier
	upstreamSlowThreshold time.Duration

	observersMu sync.RWMutex
	observers   []ClientObserver
}

// NewManager creates a new proxy manager.
// upstreamAddr is the D-Bus address for the upstream backend; empty means session bus.
// A socket whose client name another bus holds is skipped.
func NewManager(socketsDir, upstreamAddr string, approval *approval.Manager, logLevel slog.Level, upstreamNotifier UpstreamNotifier, upstreamSlowThreshold time.Duration) (*Manager, error) {
	// Create the sockets directory if it doesn't exist
	if err := os.MkdirAll(socketsDir, 0755); err != nil {
		return nil, fmt.Errorf("create sockets directory: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Manager{
		socketsDir:            socketsDir,
		upstreamAddr:          upstreamAddr,
		proxies:               proxySet{byPath: make(map[string]*proxyInstance)},
		watcher:               watcher,
		approval:              approval,
		logLevel:              logLevel,
		upstreamNotifier:      upstreamNotifier,
		upstreamSlowThreshold: upstreamSlowThreshold,
	}, nil
}

// Run starts watching for sockets and managing proxies.
// It blocks until the context is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	// Start watching the directory
	if err := m.watcher.Add(m.socketsDir); err != nil {
		return err
	}

	// Initial scan of existing sockets
	if err := m.scanExistingSockets(ctx); err != nil {
		return err
	}

	// Watch for changes
	for {
		select {
		case <-ctx.Done():
			m.stopAllProxies()
			m.watcher.Close()
			return ctx.Err()

		case event, ok := <-m.watcher.Events:
			if !ok {
				return nil
			}
			m.handleEvent(ctx, event)

		case err, ok := <-m.watcher.Errors:
			if !ok {
				return nil
			}
			slog.Error("watcher error", "error", err)
		}
	}
}

// Clients returns information about all connected clients.
func (m *Manager) Clients() []ClientInfo {
	proxies := m.proxies.snapshot()
	clients := make([]ClientInfo, 0, len(proxies))
	for path, inst := range proxies {
		clients = append(clients, ClientInfo{
			Name:       inst.proxy.clientName,
			SocketPath: path,
		})
	}
	return clients
}

// Subscribe adds an observer to receive client connection events.
func (m *Manager) Subscribe(obs ClientObserver) {
	m.observersMu.Lock()
	defer m.observersMu.Unlock()
	m.observers = append(m.observers, obs)
}

// Unsubscribe removes an observer.
func (m *Manager) Unsubscribe(obs ClientObserver) {
	m.observersMu.Lock()
	defer m.observersMu.Unlock()
	for i, o := range m.observers {
		if o == obs {
			m.observers = append(m.observers[:i], m.observers[i+1:]...)
			return
		}
	}
}

// notifyConnected notifies all observers of a client connection.
func (m *Manager) notifyConnected(client ClientInfo) {
	m.observersMu.RLock()
	defer m.observersMu.RUnlock()
	for _, obs := range m.observers {
		obs.OnClientConnected(client)
	}
}

// notifyDisconnected notifies all observers of a client disconnection.
func (m *Manager) notifyDisconnected(client ClientInfo) {
	m.observersMu.RLock()
	defer m.observersMu.RUnlock()
	for _, obs := range m.observers {
		obs.OnClientDisconnected(client)
	}
}

// scanExistingSockets connects to all existing socket files in the directory.
func (m *Manager) scanExistingSockets(ctx context.Context) error {
	entries, err := os.ReadDir(m.socketsDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if !isSocketFile(entry.Name()) {
			continue
		}

		socketPath := filepath.Join(m.socketsDir, entry.Name())
		m.startProxy(ctx, socketPath)
	}

	return nil
}

// handleEvent handles fsnotify events.
func (m *Manager) handleEvent(ctx context.Context, event fsnotify.Event) {
	if !isSocketFile(event.Name) {
		return
	}

	switch {
	case event.Has(fsnotify.Create):
		m.startProxy(ctx, event.Name)

	case event.Has(fsnotify.Remove):
		m.stopProxy(event.Name)

	case event.Has(fsnotify.Rename):
		// Rename shows up as the old name; treat as removal
		m.stopProxy(event.Name)
	}
}

// isSocketFile checks if a filename looks like a socket file.
func isSocketFile(name string) bool {
	return strings.HasSuffix(name, ".sock")
}

// startProxy starts a new proxy for the given socket.
func (m *Manager) startProxy(ctx context.Context, socketPath string) {
	if _, exists := m.proxies.get(socketPath); exists {
		return
	}

	clientName, err := approval.ClientFromSocket(socketPath)
	if err != nil {
		slog.Warn("skipping socket", "error", err)
		return
	}
	if !m.approval.ClaimClient(clientName) {
		slog.Warn("skipping socket, another bus has its client name", "socket", socketPath, "client", clientName)
		return
	}

	p := New(Config{
		ClientName:            clientName,
		LogLevel:              m.logLevel,
		Approval:              m.approval,
		UpstreamNotifier:      m.upstreamNotifier,
		UpstreamSlowThreshold: m.upstreamSlowThreshold,
	})
	if err := m.connect(p, socketPath); err != nil {
		m.approval.ReleaseClient(clientName)
		slog.Error("failed to start proxy",
			"socket", socketPath,
			"client", clientName,
			"error", err)
		return
	}

	proxyCtx, cancel := context.WithCancel(ctx)
	inst := &proxyInstance{proxy: p, cancel: cancel, done: make(chan struct{})}
	m.proxies.add(socketPath, inst)

	slog.Info("started proxy",
		"socket", socketPath,
		"client", clientName)

	m.notifyConnected(ClientInfo{Name: clientName, SocketPath: socketPath})
	go m.serve(proxyCtx, socketPath, inst)
}

// Runs inst until ctx ends or its socket closes, then gives its client name back.
func (m *Manager) serve(ctx context.Context, socketPath string, inst *proxyInstance) {
	defer close(inst.done)
	p := inst.proxy
	err := p.Run(ctx)
	if err != nil && err != context.Canceled {
		slog.Error("proxy error",
			"socket", socketPath,
			"client", p.clientName,
			"error", err)
	}
	p.Close()

	slog.Info("stopped proxy",
		"socket", socketPath,
		"client", p.clientName)

	m.notifyDisconnected(ClientInfo{Name: p.clientName, SocketPath: socketPath})
	m.approval.ReleaseClient(p.clientName)
	// Last, so that stopProxy still finds the entry, and waits on done, until the name is free.
	m.proxies.remove(socketPath)
}

// Connects p to the upstream bus and to the downstream socket.
func (m *Manager) connect(p *Proxy, socketPath string) error {
	var backendConn *dbus.Conn
	var err error
	if m.upstreamAddr == "" {
		backendConn, err = dbus.ConnectSessionBus()
	} else {
		backendConn, err = dbus.Connect(m.upstreamAddr)
	}
	if err != nil {
		return fmt.Errorf("connect to upstream %q: %w", m.upstreamAddr, err)
	}
	frontConn, err := dbus.Connect("unix:path=" + socketPath)
	if err != nil {
		backendConn.Close()
		return fmt.Errorf("connect to downstream socket: %w", err)
	}
	return p.ConnectWith(frontConn, backendConn)
}

// Stops the proxy for socketPath and returns once its client name is free.
func (m *Manager) stopProxy(socketPath string) {
	if inst, exists := m.proxies.get(socketPath); exists {
		inst.cancel()
		<-inst.done
	}
}

// Stops all running proxies and returns once their client names are free.
func (m *Manager) stopAllProxies() {
	proxies := slices.Collect(maps.Values(m.proxies.snapshot()))

	for _, inst := range proxies {
		inst.cancel()
	}
	for _, inst := range proxies {
		<-inst.done
	}
}
