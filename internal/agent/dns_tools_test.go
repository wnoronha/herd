package agent

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
)

func TestBuildDefaultToolsIncludesDNSLookup(t *testing.T) {
	for _, tool := range BuildDefaultTools(nil) {
		if tool.Name == "dns_lookup" {
			return
		}
	}
	t.Fatal("dns_lookup missing from BuildDefaultTools")
}

// requireLiveDNS skips live-resolver tests unless HERD_LIVE_DNS=1, keeping
// the default suite hermetic (CI has no external DNS guarantee).
func requireLiveDNS(t *testing.T) {
	t.Helper()
	if os.Getenv("HERD_LIVE_DNS") == "" {
		t.Skip("skipping live-DNS test (set HERD_LIVE_DNS=1 to run)")
	}
}

func TestDNSLookupToolDefinition(t *testing.T) {
	tool := buildDNSLookupTool()
	if tool.Name != "dns_lookup" {
		t.Fatalf("unexpected tool name: %q", tool.Name)
	}
	if tool.Handler == nil {
		t.Fatal("handler must not be nil")
	}
	props, ok := tool.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in parameters schema")
	}
	if _, ok := props["name"]; !ok {
		t.Fatal("expected name property in schema")
	}
}

func TestValidateDNSLookupName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"example.com", false},
		{"_dmarc.example.com", false},
		{"192.0.2.1", false},
		{"has space.example.com", true},
		{"bad/name", true},
	}
	for _, tc := range cases {
		err := validateDNSLookupName(tc.name)
		if tc.wantErr && err == nil {
			t.Errorf("expected error for name %q", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("unexpected error for name %q: %v", tc.name, err)
		}
	}
}

func TestDNSLookupToolValidationErrors(t *testing.T) {
	tool := buildDNSLookupTool()
	ctx := context.Background()

	_, err := tool.Handler(ctx, map[string]any{"name": ""})
	if err == nil {
		t.Fatal("expected error for empty name")
	}

	longName := strings.Repeat("a", 254)
	_, err = tool.Handler(ctx, map[string]any{"name": longName})
	if err == nil {
		t.Fatal("expected error for overlong name")
	}

	_, err = tool.Handler(ctx, map[string]any{
		"name":        "example.com",
		"record_type": "NOTATYPE",
	})
	if err == nil {
		t.Fatal("expected error for unsupported record type")
	}

	_, err = tool.Handler(ctx, map[string]any{"name": "bad host.example.com"})
	if err == nil {
		t.Fatal("expected error for name with space")
	}
}

func TestExecuteDNSLookupUnsupportedType(t *testing.T) {
	r := net.DefaultResolver
	_, err := executeDNSLookup(context.Background(), r, "example.com", "SOA")
	if err == nil || !strings.Contains(err.Error(), "unsupported record_type") {
		t.Fatalf("expected unsupported type error, got: %v", err)
	}
}

func TestDNSLookupToolIntegrationExampleCom(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DNS integration test in -short mode")
	}
	requireLiveDNS(t)

	tool := buildDNSLookupTool()
	res, err := tool.Handler(context.Background(), map[string]any{
		"name":        "example.com",
		"record_type": "A",
	})
	if err != nil {
		t.Skipf("dns lookup unavailable: %v", err)
	}
	m := res.(map[string]any)
	if m["record_count"].(int) == 0 {
		t.Fatalf("expected at least one A record for example.com")
	}
	switch records := m["records"].(type) {
	case []map[string]any:
		if len(records) == 0 || records[0]["type"] != "A" {
			t.Fatalf("expected A record entry, got: %v", records)
		}
	case []any:
		if len(records) == 0 {
			t.Fatalf("expected records slice")
		}
	default:
		t.Fatalf("unexpected records type: %T", m["records"])
	}
}

func TestDNSLookupToolIntegrationTXT(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DNS integration test in -short mode")
	}
	requireLiveDNS(t)

	tool := buildDNSLookupTool()
	res, err := tool.Handler(context.Background(), map[string]any{
		"name":        "example.com",
		"record_type": "TXT",
	})
	if err != nil {
		t.Skipf("dns lookup unavailable: %v", err)
	}
	m := res.(map[string]any)
	if m["record_type"] != "TXT" {
		t.Fatalf("unexpected record_type: %v", m["record_type"])
	}
}
