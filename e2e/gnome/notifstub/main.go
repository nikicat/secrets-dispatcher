// notifstub owns org.freedesktop.Notifications on the session bus and prints
// one flat, greppable line per Notify call — the Tier-1 capture stub for the
// US-7 gates (fast.sh): it asserts what the dispatcher actually sends
// (expire_timeout=0, urgency=critical, Approve/Deny actions) without any real
// notification daemon in the loop.
//
// Output format (stdout, one line per call, flushed immediately):
//
//	NOTIFY app=... expire_timeout=0 urgency=2 actions=approve,Approve,... summary=...
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	dest  = "org.freedesktop.Notifications"
	path  = "/org/freedesktop/Notifications"
	iface = "org.freedesktop.Notifications"
)

type server struct {
	conn   *dbus.Conn
	nextID uint32
}

func (s *server) Notify(appName string, replacesID uint32, icon, summary, body string,
	actions []string, hints map[string]dbus.Variant, expireTimeout int32) (uint32, *dbus.Error) {
	urgency := "-"
	if v, ok := hints["urgency"]; ok {
		urgency = fmt.Sprintf("%v", v.Value())
	}
	fmt.Printf("NOTIFY app=%s expire_timeout=%d urgency=%s actions=%s summary=%q body_len=%d\n",
		appName, expireTimeout, urgency, strings.Join(actions, ","), summary, len(body))
	s.nextID++
	return s.nextID, nil
}

func (s *server) CloseNotification(id uint32) *dbus.Error {
	fmt.Printf("CLOSE id=%d\n", id)
	_ = s.conn.Emit(path, iface+".NotificationClosed", id, uint32(3))
	return nil
}

func (s *server) GetCapabilities() ([]string, *dbus.Error) {
	return []string{"actions", "body", "persistence"}, nil
}

func (s *server) GetServerInformation() (string, string, string, string, *dbus.Error) {
	return "notifstub", "secrets-dispatcher-e2e", "1.0", "1.2", nil
}

func main() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "notifstub: connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	s := &server{conn: conn}
	if err := conn.Export(s, path, iface); err != nil {
		fmt.Fprintf(os.Stderr, "notifstub: export: %v\n", err)
		os.Exit(1)
	}
	reply, err := conn.RequestName(dest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		fmt.Fprintf(os.Stderr, "notifstub: cannot own %s (reply=%v err=%v)\n", dest, reply, err)
		os.Exit(1)
	}
	fmt.Println("READY")
	select {}
}
