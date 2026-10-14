package cli

import (
	"os"
	"testing"
)

// TestMain keeps CLI tests hermetic: daemons started here must not announce
// on or browse LAN multicast (they would discover each other across tests).
func TestMain(m *testing.M) {
	_ = os.Setenv("HERD_MDNS", "0")
	os.Exit(m.Run())
}
