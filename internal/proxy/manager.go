package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

type ClientInfo struct {
	Name       approval.ClientName `json:"name"`
	SocketPath unixsock.Path       `json:"socket_path"`
}

type ClientObserver interface {
	OnClientConnected(client ClientInfo)
	OnClientDisconnected(client ClientInfo)
}

type proxyInstance struct {
	proxy  *Proxy
	claim  Claim
	cancel context.CancelFunc
	done   chan struct{} // closed once the proxy has closed and its name is free
}

// The running proxies by socket path.
type proxySet struct {
	mu     sync.RWMutex
	byPath map[unixsock.Path]*proxyInstance
}

func (s *proxySet) get(path unixsock.Path) (*proxyInstance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.byPath[path]
	return inst, ok
}

func (s *proxySet) add(path unixsock.Path, inst *proxyInstance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byPath[path] = inst
}

func (s *proxySet) remove(path unixsock.Path) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byPath, path)
}

// Returns a copy that later changes do not affect.
func (s *proxySet) snapshot() map[unixsock.Path]*proxyInstance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.byPath)
}

// Runs a proxy for each socket in a directory while the socket exists, under
// the socket's client name. A socket whose client name another bus holds is
// skipped.
type Manager struct {
	socketsDir unixsock.Dir
	factory    Factory
	proxies    proxySet
	watcher    *fsnotify.Watcher

	observersMu sync.RWMutex
	observers   []ClientObserver
}

// Creates the sockets directory if it does not exist.
func NewManager(socketsDir unixsock.Dir, factory Factory) (*Manager, error) {
	if err := socketsDir.MkdirAll(0755); err != nil {
		return nil, fmt.Errorf("create sockets directory: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Manager{
		socketsDir: socketsDir,
		factory:    factory,
		proxies:    proxySet{byPath: make(map[unixsock.Path]*proxyInstance)},
		watcher:    watcher,
	}, nil
}

// Blocks until the given context ends and returns its error. Errors when the
// sockets directory cannot be watched or read.
func (m *Manager) Run(ctx context.Context) error {
	// Start watching the directory
	if err := m.watcher.Add(m.socketsDir.String()); err != nil {
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

func (m *Manager) Clients() []ClientInfo {
	proxies := m.proxies.snapshot()
	clients := make([]ClientInfo, 0, len(proxies))
	for path, inst := range proxies {
		clients = append(clients, ClientInfo{
			Name:       inst.proxy.claim.Name,
			SocketPath: path,
		})
	}
	return clients
}

// Tells the given observer of each later connect and disconnect.
func (m *Manager) Subscribe(obs ClientObserver) {
	m.observersMu.Lock()
	defer m.observersMu.Unlock()
	m.observers = append(m.observers, obs)
}

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

func (m *Manager) notifyConnected(client ClientInfo) {
	m.observersMu.RLock()
	defer m.observersMu.RUnlock()
	for _, obs := range m.observers {
		obs.OnClientConnected(client)
	}
}

func (m *Manager) notifyDisconnected(client ClientInfo) {
	m.observersMu.RLock()
	defer m.observersMu.RUnlock()
	for _, obs := range m.observers {
		obs.OnClientDisconnected(client)
	}
}

func (m *Manager) scanExistingSockets(ctx context.Context) error {
	entries, err := m.socketsDir.ReadDir()
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if socket := m.socketsDir.Path(entry.Name()); socket.IsSocketFile() {
			m.startProxy(ctx, socket)
		}
	}

	return nil
}

func (m *Manager) handleEvent(ctx context.Context, event fsnotify.Event) {
	socket := unixsock.Path(event.Name)
	if !socket.IsSocketFile() {
		return
	}

	switch {
	case event.Has(fsnotify.Create):
		m.startProxy(ctx, socket)

	case event.Has(fsnotify.Remove):
		m.stopProxy(socket)

	case event.Has(fsnotify.Rename):
		// Rename shows up as the old name; treat as removal
		m.stopProxy(socket)
	}
}

// Starts a proxy for the given socket unless one runs already.
func (m *Manager) startProxy(ctx context.Context, socketPath unixsock.Path) {
	if _, exists := m.proxies.get(socketPath); exists {
		return
	}

	clientName, err := approval.ClientFromSocket(socketPath)
	if err != nil {
		slog.Warn("skipping socket", "error", err)
		return
	}
	claim, err := m.factory.Claim(clientName)
	if err != nil {
		slog.Warn("skipping socket", "socket", socketPath, "error", err)
		return
	}

	p, err := claim.Dial(socketPath.ConnectDBus)
	if err != nil {
		claim.Release()
		slog.Error("failed to start proxy",
			"socket", socketPath,
			"client", clientName,
			"error", err)
		return
	}

	proxyCtx, cancel := context.WithCancel(ctx)
	inst := &proxyInstance{proxy: p, claim: claim, cancel: cancel, done: make(chan struct{})}
	m.proxies.add(socketPath, inst)

	slog.Info("started proxy",
		"socket", socketPath,
		"client", clientName)

	m.notifyConnected(ClientInfo{Name: clientName, SocketPath: socketPath})
	go m.serve(proxyCtx, socketPath, inst)
}

// Runs the given proxy until the context ends or its socket closes, then gives
// its client name back.
func (m *Manager) serve(ctx context.Context, socketPath unixsock.Path, inst *proxyInstance) {
	defer close(inst.done)
	p := inst.proxy
	err := p.Run(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("proxy error",
			"socket", socketPath,
			"client", p.claim.Name,
			"error", err)
	}
	p.Close()

	slog.Info("stopped proxy",
		"socket", socketPath,
		"client", p.claim.Name)

	m.notifyDisconnected(ClientInfo{Name: p.claim.Name, SocketPath: socketPath})
	inst.claim.Release()
	// Last, so that stopProxy still finds the entry, and waits on done, until the name is free.
	m.proxies.remove(socketPath)
}

// Stops the proxy of the given socket and returns once its client name is free.
func (m *Manager) stopProxy(socketPath unixsock.Path) {
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
