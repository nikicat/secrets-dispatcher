// mock-secret-service runs a minimal Secret Service for testing.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

func main() {
	var (
		addItem = flag.String("add-item", "", "Add a test item with format 'label:attr1=val1,attr2=val2:secret'")
	)
	flag.Parse()

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connect to session bus: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	mock := testutil.NewMockSecretService()
	if err := mock.Register(conn); err != nil {
		fmt.Fprintf(os.Stderr, "error: register mock service: %v\n", err)
		os.Exit(1)
	}

	// Add a test item if requested
	if *addItem != "" {
		// Parse format: label:attr1=val1,attr2=val2:secret
		// For simplicity, just add a default test item
		mock.AddItem("Test Secret", map[string]string{"test-attr": "test-value"}, []byte("test-secret"))
	}

	fmt.Println("Mock Secret Service running. Press Ctrl+C to exit.")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		fmt.Println("Shutting down...")
	case <-ctx.Done():
	}
}
