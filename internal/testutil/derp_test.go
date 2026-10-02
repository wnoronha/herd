package testutil

import (
	"io"
	"net/http"
	"os"
	"testing"
)

func TestLocalDERPHelper(t *testing.T) {
	ld, cleanup := StartLocalDERP(t)
	defer cleanup()

	if os.Getenv("IN_TS_TEST") != "true" {
		t.Errorf("expected IN_TS_TEST=true in hermetic environment")
	}

	if ld.DERPMap == nil || len(ld.DERPMap.Regions) == 0 {
		t.Fatalf("expected valid DERPMap with local region")
	}

	// Verify HTTP endpoint works locally
	resp, err := http.Get(ld.Addr)
	if err != nil {
		t.Fatalf("failed to query local DERP HTTP endpoint: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	if string(body) != "Herd Local Hermetic Relay OK\n" {
		t.Errorf("unexpected body: %s", string(body))
	}
}
