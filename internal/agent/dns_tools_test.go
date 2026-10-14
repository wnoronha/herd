package agent

import (
	"context"
	"testing"
)

func TestDNSLookupToolExampleCom(t *testing.T) {
	tool := buildDNSLookupTool()
	ctx := context.Background()

	res, err := tool.Handler(ctx, map[string]any{
		"name":         "example.com",
		"record_type":  "A",
	})
	if err != nil {
		t.Skipf("dns lookup unavailable: %v", err)
	}
	m := res.(map[string]any)
	if m["record_count"].(int) == 0 {
		t.Fatalf("expected at least one A record for example.com")
	}

	_, err = tool.Handler(ctx, map[string]any{"name": ""})
	if err == nil {
		t.Fatal("expected error for empty name")
	}

	_, err = tool.Handler(ctx, map[string]any{
		"name":         "example.com",
		"record_type":  "NOTATYPE",
	})
	if err == nil {
		t.Fatal("expected error for unsupported record type")
	}
}

func TestDNSLookupToolTXT(t *testing.T) {
	tool := buildDNSLookupTool()
	res, err := tool.Handler(context.Background(), map[string]any{
		"name":        "_dmarc.example.com",
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
