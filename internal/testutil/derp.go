package testutil

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// DERPRegion represents a local mock DERP region for testing.
type DERPRegion struct {
	RegionID   int      `json:"region_id"`
	RegionCode string   `json:"region_code"`
	RegionName string   `json:"region_name"`
	Nodes      []string `json:"nodes"`
}

// DERPMap holds regional mapping for DERP relays.
type DERPMap struct {
	Regions map[int]*DERPRegion `json:"regions"`
}

// LocalDERP represents a running in-process hermetic DERP/STUN test server.
type LocalDERP struct {
	HTTPServer *httptest.Server
	STUNConn   *net.UDPConn
	DERPMap    *DERPMap
	Addr       string
}

// Close stops the mock DERP/STUN listeners.
func (ld *LocalDERP) Close() {
	if ld.HTTPServer != nil {
		ld.HTTPServer.Close()
	}
	if ld.STUNConn != nil {
		_ = ld.STUNConn.Close()
	}
}

// SetupHermeticEnvironment sets standard environment flags to keep tests completely offline.
func SetupHermeticEnvironment(t testing.TB) {
	t.Helper()
	t.Setenv("IN_TS_TEST", "true")
	t.Setenv("HERD_DEV_LOCAL_DERP", "1")
	t.Setenv("TS_DISABLE_UPNP", "true")
	t.Setenv("TS_DEBUG_NETCHECK", "0")
}

// StartLocalDERP spins up a local in-process HTTP and STUN relay for hermetic multi-node testing.
func StartLocalDERP(t testing.TB) (*LocalDERP, func()) {
	t.Helper()
	SetupHermeticEnvironment(t)

	// Mock STUN UDP listener on 127.0.0.1:0
	stunAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to resolve local STUN UDP addr: %v", err)
	}
	stunConn, err := net.ListenUDP("udp", stunAddr)
	if err != nil {
		t.Fatalf("failed to start mock STUN listener: %v", err)
	}

	// Mock HTTP DERP Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Herd-DERP", "local-hermetic-relay")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "Herd Local Hermetic Relay OK")
	})
	ts := httptest.NewServer(handler)

	dMap := &DERPMap{
		Regions: map[int]*DERPRegion{
			1: {
				RegionID:   1,
				RegionCode: "local-dev",
				RegionName: "Local Hermetic Test Relay",
				Nodes:      []string{ts.URL, stunConn.LocalAddr().String()},
			},
		},
	}

	ld := &LocalDERP{
		HTTPServer: ts,
		STUNConn:   stunConn,
		DERPMap:    dMap,
		Addr:       ts.URL,
	}

	cleanup := func() {
		ld.Close()
	}

	return ld, cleanup
}

// EnsureHermeticTestMain can be invoked in TestMain to prevent accidental external network probing.
func EnsureHermeticTestMain() {
	_ = os.Setenv("IN_TS_TEST", "true")
	_ = os.Setenv("HERD_DEV_LOCAL_DERP", "1")
	_ = os.Setenv("TS_DISABLE_UPNP", "true")
}
