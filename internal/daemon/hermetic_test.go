package daemon

import (
	"os"
	"testing"
)

// TestMain keeps daemon unit tests hermetic: real daemons started here must
// not announce on or browse LAN multicast (they would discover each other
// across tests). Tests for discovery itself opt back in explicitly.
func TestMain(m *testing.M) {
	_ = os.Setenv("HERD_MDNS", "0")
	os.Exit(m.Run())
}
