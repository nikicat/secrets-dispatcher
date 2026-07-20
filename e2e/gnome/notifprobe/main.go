// notifprobe is the Tier-2 US-7 acceptance gate, run inside the desktop VM
// against REAL gnome-shell.
//
// Platform model (read from gnome-shell 46 sources, verified empirically):
// gnome-shell IGNORES expire_timeout entirely (the fdo daemon discards it).
// A critical, non-transient notification — our approval shape — persists
// until acted on or explicitly closed. Its only close-with-reason=expired
// pathways are the `transient` hint and MAX_NOTIFICATIONS_PER_SOURCE=3
// eviction (a 4th open notification from one app destroys that app's
// oldest). The expire_timeout=0 our notifier sends matters on spec-honoring
// daemons (dunst, mako, KDE close a -1 notification after their default
// timeout); on GNOME what protects approvals is critical + non-transient.
//
// Phases, each observing on its own sending connection (NotificationClosed
// and ActionInvoked are unicast to the Notify caller — nothing else ever
// sees them):
//
//  1. Sanity echo: Notify + CloseNotification must produce
//     NotificationClosed(id, reason=3) here. Proves the notification daemon
//     is up (gjs bridge exists) and close signals are observable — without
//     this, phase 3 could pass vacuously.
//  2. Fidelity check: GetServerInformation must identify gnome-shell (we are
//     testing the real desktop, not a stub).
//  3. Production assertion: send through the real notification.DBusNotifier
//     (the code path `serve` uses) and require NO close of any kind within
//     the window. Catches regressions in the urgency/transient hints (which
//     would let gnome-shell hide or expire the banner) and, on daemons that
//     honor expire_timeout, a regression back to -1.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/nikicat/secrets-dispatcher/internal/notification"
)

const (
	dest  = "org.freedesktop.Notifications"
	path  = dbus.ObjectPath("/org/freedesktop/Notifications")
	iface = "org.freedesktop.Notifications"

	reasonClosed = uint32(3) // explicit CloseNotification
)

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "notifprobe: FAIL: "+format+"\n", args...)
	os.Exit(1)
}

// sanityEcho proves close signals are observable: a notification we close
// ourselves must echo NotificationClosed(reason=3) back on this connection.
func sanityEcho() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		die("connect: %v", err)
	}
	defer conn.Close()

	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(iface),
		dbus.WithMatchMember("NotificationClosed"),
	); err != nil {
		die("subscribe: %v", err)
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)

	var id uint32
	call := conn.Object(dest, path).Call(iface+".Notify", 0,
		"notifprobe-sanity", uint32(0), "dialog-password",
		"notifprobe sanity (closed immediately)", "close-echo check",
		[]string{}, map[string]dbus.Variant{}, int32(-1))
	if call.Err != nil {
		die("sanity Notify: %v (gjs bridge missing? org.freedesktop.Notifications unservable)", call.Err)
	}
	if err := call.Store(&id); err != nil {
		die("sanity Notify result: %v", err)
	}
	if c := conn.Object(dest, path).Call(iface+".CloseNotification", 0, id); c.Err != nil {
		die("sanity CloseNotification: %v", c.Err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case sig := <-signals:
			if sig.Name != iface+".NotificationClosed" || len(sig.Body) != 2 {
				continue
			}
			sigID, _ := sig.Body[0].(uint32)
			reason, _ := sig.Body[1].(uint32)
			if sigID != id {
				continue
			}
			if reason != reasonClosed {
				die("sanity close echoed reason=%d, want %d", reason, reasonClosed)
			}
			fmt.Println("notifprobe: sanity: close signals are observable on the sender connection")
			return
		case <-deadline:
			die("no NotificationClosed echo within 10s — close signals unobservable, " +
				"the production assertion would be meaningless")
		}
	}
}

// serverIdentity returns the notification server's name (e.g. "gnome-shell").
func serverIdentity() string {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		die("connect: %v", err)
	}
	defer conn.Close()

	var name, vendor, version, specVersion string
	call := conn.Object(dest, path).Call(iface+".GetServerInformation", 0)
	if call.Err != nil {
		die("GetServerInformation: %v", call.Err)
	}
	if err := call.Store(&name, &vendor, &version, &specVersion); err != nil {
		die("GetServerInformation result: %v", err)
	}
	return fmt.Sprintf("%s %s (%s)", name, version, vendor)
}

func main() {
	window := 10 * time.Second
	if v := os.Getenv("NOTIFPROBE_WINDOW"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			die("bad NOTIFPROBE_WINDOW %q: %v", v, err)
		}
		window = d
	}

	sanityEcho()

	server := serverIdentity()
	fmt.Printf("notifprobe: notification server: %s\n", server)
	if !strings.Contains(server, "gnome-shell") {
		die("server is %q, want gnome-shell — this gate exists to test the real desktop", server)
	}

	notifier, err := notification.NewDBusNotifier()
	if err != nil {
		die("NewDBusNotifier: %v", err)
	}
	defer notifier.Stop()

	id, err := notifier.Notify("Secret access requested (notifprobe)",
		"production-path notification: must survive untouched",
		"dialog-password",
		[]string{"approve", "Approve", "deny", "Deny"})
	if err != nil {
		die("Notify: %v", err)
	}

	timer := time.NewTimer(window)
	defer timer.Stop()
	for {
		select {
		case c := <-notifier.ClosedEvents():
			if c.NotificationID != id {
				continue
			}
			die("production notification closed after <%s with reason=%d — "+
				"approvals must persist until acted on (US-7)", window, c.Reason)
		case <-timer.C:
			// Survived the window: clean up our banner and pass.
			_ = notifier.Close(id)
			fmt.Printf("notifprobe: PASS: production notification survived %s on %s\n",
				window, server)
			return
		}
	}
}
