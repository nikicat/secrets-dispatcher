package proxy

import (
	"strings"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// signalForwarder subscribes to signals from the Secret Service on the backend
// connection and re-emits them on the frontend connection. This enables clients
// like Seahorse to see live updates when secrets are created/deleted.
//
// Note: Seahorse has a bug where adding an item switches the view to the first
// collection alphabetically. This is caused by Seahorse's own focus-place action,
// not by forwarded signals. See https://gitlab.gnome.org/GNOME/seahorse/-/issues/430
type signalForwarder struct {
	backendConn *dbus.Conn
	frontConn   *dbus.Conn
	logger      *logging.Logger
	ch          chan *dbus.Signal
	done        chan struct{}
}

func newSignalForwarder(backendConn, frontConn *dbus.Conn, logger *logging.Logger) (*signalForwarder, error) {
	f := &signalForwarder{
		backendConn: backendConn,
		frontConn:   frontConn,
		logger:      logger,
		ch:          make(chan *dbus.Signal, 64),
		done:        make(chan struct{}),
	}

	if err := backendConn.AddMatchSignal(
		dbus.WithMatchSender(dbustypes.BusName),
	); err != nil {
		return nil, err
	}
	backendConn.Signal(f.ch)

	go f.run()
	return f, nil
}

func (f *signalForwarder) run() {
	for {
		select {
		case sig, ok := <-f.ch:
			if !ok {
				return
			}
			// Skip D-Bus daemon signals (NameAcquired, NameOwnerChanged, etc.)
			if !strings.HasPrefix(sig.Name, "org.freedesktop.Secret.") &&
				!strings.HasPrefix(sig.Name, "org.freedesktop.DBus.Properties.") {
				continue
			}
			if err := f.frontConn.Emit(sig.Path, sig.Name, sig.Body...); err != nil {
				f.logger.Info("failed to forward signal", "signal", sig.Name, "path", sig.Path, "error", err)
			} else {
				f.logger.Info("forwarded signal", "signal", sig.Name, "path", sig.Path)
			}
		case <-f.done:
			return
		}
	}
}

func (f *signalForwarder) close() {
	close(f.done)
	f.backendConn.RemoveSignal(f.ch)
}
