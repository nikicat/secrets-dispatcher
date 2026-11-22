package proxy

import (
	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// Re-emits the backend Secret Service's signals on the front bus, so clients
// see items and collections change live.
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
			// The bus daemon sends NameAcquired and NameLost to every connection
			// without a match rule.
			if sig.Sender == dbustypes.BusDaemonName {
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
