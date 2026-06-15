package proxy

import (
	"sync"

	"github.com/godbus/dbus/v5"
)

type promptRegistry struct {
	mu     sync.Mutex
	owners map[dbus.ObjectPath]string
}

func newPromptRegistry() *promptRegistry {
	return &promptRegistry{owners: make(map[dbus.ObjectPath]string)}
}

func (r *promptRegistry) register(path dbus.ObjectPath, sender string) {
	if r == nil || path == "" || path == "/" || sender == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.owners[path] = sender
}

func (r *promptRegistry) owner(path dbus.ObjectPath) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, ok := r.owners[path]
	return owner, ok
}

func (r *promptRegistry) unregister(path dbus.ObjectPath) {
	if r == nil || path == "" || path == "/" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.owners, path)
}
