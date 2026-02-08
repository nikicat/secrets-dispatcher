package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
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
}

// Manager watches a directory for socket files and manages proxy connections.
type Manager struct {
	socketsDir string
	proxies    map[string]*proxyInstance // socketPath -> proxyInstance
	mu         sync.RWMutex
	watcher    *fsnotify.Watcher
	approval   *approval.Manager
	logLevel   slog.Level

	observersMu sync.RWMutex
	observers   []ClientObserver
}

// NewManager creates a new proxy manager.
func NewManager(socketsDir string, approval *approval.Manager, logLevel slog.Level) (*Manager, error) {
	// Create the sockets directory if it doesn't exist
	if err := os.MkdirAll(socketsDir, 0755); err != nil {
		return nil, fmt.Errorf("create sockets directory: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Manager{
		socketsDir: socketsDir,
		proxies:    make(map[string]*proxyInstance),
		watcher:    watcher,
		approval:   approval,
		logLevel:   logLevel,
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
	m.mu.RLock()
	defer m.mu.RUnlock()

	clients := make([]ClientInfo, 0, len(m.proxies))
	for path, inst := range m.proxies {
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

// clientNameFromSocket derives a client name from a socket filename.
func clientNameFromSocket(socketPath string) string {
	base := filepath.Base(socketPath)
	return strings.TrimSuffix(base, ".sock")
}

// startProxy starts a new proxy for the given socket.
func (m *Manager) startProxy(ctx context.Context, socketPath string) {
	m.mu.Lock()
	if _, exists := m.proxies[socketPath]; exists {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	clientName := clientNameFromSocket(socketPath)

	cfg := Config{
		RemoteSocketPath: socketPath,
		ClientName:       clientName,
		LogLevel:         m.logLevel,
		Approval:         m.approval,
	}

	p := New(cfg)

	proxyCtx, cancel := context.WithCancel(ctx)

	// Try to connect
	if err := p.Connect(proxyCtx); err != nil {
		slog.Error("failed to connect proxy",
			"socket", socketPath,
			"client", clientName,
			"error", err)
		cancel()
		return
	}

	m.mu.Lock()
	m.proxies[socketPath] = &proxyInstance{
		proxy:  p,
		cancel: cancel,
	}
	m.mu.Unlock()

	client := ClientInfo{Name: clientName, SocketPath: socketPath}

	slog.Info("started proxy",
		"socket", socketPath,
		"client", clientName)

	// Notify observers of new client
	m.notifyConnected(client)

	// Run the proxy in a goroutine
	go func() {
		err := p.Run(proxyCtx)
		if err != nil && err != context.Canceled {
			slog.Error("proxy error",
				"socket", socketPath,
				"client", clientName,
				"error", err)
		}
		p.Close()

		// Remove from map when done
		m.mu.Lock()
		delete(m.proxies, socketPath)
		m.mu.Unlock()

		slog.Info("stopped proxy",
			"socket", socketPath,
			"client", clientName)

		// Notify observers of client disconnect
		m.notifyDisconnected(client)
	}()
}

// stopProxy stops the proxy for the given socket.
func (m *Manager) stopProxy(socketPath string) {
	m.mu.Lock()
	inst, exists := m.proxies[socketPath]
	if exists {
		delete(m.proxies, socketPath)
	}
	m.mu.Unlock()

	if exists {
		inst.cancel()
		// Proxy cleanup happens in the goroutine
	}
}

// stopAllProxies stops all running proxies.
func (m *Manager) stopAllProxies() {
	m.mu.Lock()
	proxies := make([]*proxyInstance, 0, len(m.proxies))
	for _, inst := range m.proxies {
		proxies = append(proxies, inst)
	}
	m.proxies = make(map[string]*proxyInstance)
	m.mu.Unlock()

	for _, inst := range proxies {
		inst.cancel()
	}
}
