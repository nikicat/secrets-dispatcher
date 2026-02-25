package daemon

import (
	"log/slog"
	"net"
	"os"
)

// SdNotify sends a state notification to systemd via NOTIFY_SOCKET.
// If NOTIFY_SOCKET is not set (non-systemd environment), returns silently.
// Dial failures are logged as warnings but do not return an error (fire-and-forget).
func SdNotify(state string) {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return
	}
	conn, err := net.Dial("unixgram", socket)
	if err != nil {
		slog.Warn("sd-notify dial failed", "socket", socket, "err", err)
		return
	}
	defer conn.Close()
	conn.Write([]byte(state)) //nolint:errcheck
}
