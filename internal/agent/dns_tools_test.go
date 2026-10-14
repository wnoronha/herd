package agent

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"strings"
	"testing"
	"time"
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
		{"evil.example.com;id", true},
		{"a$(id).example.com", true},
		{"a|b.example.com", true},
		{"back`tick.example.com", true},
		{"under!bang.example.com", true},
		{"double..dots.example.com", true},
		{"localhost", true},
		{"svc.cluster.local", true},
		{"db.internal", true},
		{"singlelabel", true},
		{"127.0.0.1", true},
		{"10.0.0.5", false},
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
	_, err := executeDNSLookup(context.Background(), r, "example.com", "SOA", "", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported record_type") {
		t.Fatalf("expected unsupported type error, got: %v", err)
	}
}

func TestDNSLookupSRVRequiresServiceProto(t *testing.T) {
	tool := buildDNSLookupTool()
	ctx := context.Background()

	_, err := tool.Handler(ctx, map[string]any{"name": "example.com", "record_type": "SRV"})
	if err == nil || !strings.Contains(err.Error(), "requires 'service' and 'proto'") {
		t.Fatalf("expected SRV service/proto error, got: %v", err)
	}

	_, err = tool.Handler(ctx, map[string]any{
		"name": "example.com", "record_type": "SRV", "service": "http",
	})
	if err == nil || !strings.Contains(err.Error(), "both 'service' and 'proto'") {
		t.Fatalf("expected SRV partial-params error, got: %v", err)
	}

	_, err = tool.Handler(ctx, map[string]any{
		"name": "example.com", "record_type": "A", "service": "http", "proto": "tcp",
	})
	if err == nil || !strings.Contains(err.Error(), "SRV lookups only") {
		t.Fatalf("expected service/proto scope error, got: %v", err)
	}
}

func TestInternalNameOverride(t *testing.T) {
	t.Setenv("HERD_ALLOW_INTERNAL_DNS", "1")
	if err := validateDNSLookupName("localhost"); err != nil {
		t.Fatalf("expected override to allow localhost, got: %v", err)
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
		// Past requireLiveDNS the flag is set: fail, don't skip.
		t.Fatalf("live dns lookup failed: %v", err)
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
		// Past requireLiveDNS the flag is set: fail, don't skip.
		t.Fatalf("live dns lookup failed: %v", err)
	}
	m := res.(map[string]any)
	if m["record_type"] != "TXT" {
		t.Fatalf("unexpected record_type: %v", m["record_type"])
	}
}

// TestExecuteDNSLookupHermeticStub exercises the A/AUTO lookup path against
// an in-test stub DNS server. It always runs (no network, no flag): this is
// the coverage that must never silently skip.
func TestExecuteDNSLookupHermeticStub(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("stub dns listen: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go serveStubDNS(pc)

	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", pc.LocalAddr().String())
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := executeDNSLookup(ctx, resolver, "probe.test.", "A", "", "")
	if err != nil {
		t.Fatalf("stub A lookup: %v", err)
	}
	if out["record_count"] != 1 {
		t.Fatalf("expected 1 stub A record, got: %v", out)
	}
	recs, ok := out["records"].([]map[string]any)
	if !ok || len(recs) != 1 || recs[0]["value"] != "192.0.2.1" {
		t.Fatalf("unexpected stub A records: %v", out["records"])
	}

	out, err = executeDNSLookup(ctx, resolver, "probe.test.", "AUTO", "", "")
	if err != nil {
		t.Fatalf("stub AUTO lookup: %v", err)
	}
	if out["record_count"].(int) < 1 {
		t.Fatalf("expected stub AUTO records, got: %v", out)
	}
}

// serveStubDNS answers A queries with 192.0.2.1 (TEST-NET-1) and NODATA for
// anything else, until the packet conn closes.
func serveStubDNS(pc net.PacketConn) {
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		qend, qtype, ok := dnsQuestionEnd(buf[:n])
		if !ok {
			continue
		}
		resp := buildStubDNSResponse(buf[:n], qend, qtype)
		_, _ = pc.WriteTo(resp, addr)
	}
}

// dnsQuestionEnd locates the end of the question section (QNAME + QTYPE +
// QCLASS), skipping any EDNS OPT record the resolver appends.
func dnsQuestionEnd(query []byte) (int, uint16, bool) {
	off := 12
	for {
		if off >= len(query) {
			return 0, 0, false
		}
		l := int(query[off])
		off++
		if l == 0 {
			break
		}
		if l&0xC0 != 0 {
			return 0, 0, false
		}
		off += l
	}
	if off+4 > len(query) {
		return 0, 0, false
	}
	return off + 4, binary.BigEndian.Uint16(query[off:]), true
}

func buildStubDNSResponse(query []byte, qend int, qtype uint16) []byte {
	resp := make([]byte, 0, len(query)+16)
	resp = append(resp, query[0:2]...) // ID
	resp = append(resp, 0x81, 0x80)    // standard response, no error
	resp = append(resp, query[4:6]...) // QDCOUNT
	if qtype == 1 {
		resp = append(resp, 0x00, 0x01) // ANCOUNT = 1
	} else {
		resp = append(resp, 0x00, 0x00) // NODATA
	}
	resp = append(resp, 0x00, 0x00, 0x00, 0x00) // NSCOUNT, ARCOUNT
	resp = append(resp, query[12:qend]...)      // question echo (no OPT)
	if qtype == 1 {
		resp = append(resp,
			0xC0, 0x0C, // pointer to QNAME
			0x00, 0x01, // TYPE A
			0x00, 0x01, // CLASS IN
			0x00, 0x00, 0x00, 0x3C, // TTL 60
			0x00, 0x04, // RDLENGTH
			192, 0, 2, 1, // TEST-NET-1
		)
	}
	return resp
}
