package agent

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// buildDNSLookupTool returns an agent tool for resolving DNS records.
func buildDNSLookupTool() ToolDefinition {
	return ToolDefinition{
		Name:        "dns_lookup",
		Description: "Look up DNS records for a hostname or IP address (A, AAAA, MX, TXT, NS, CNAME, PTR, SRV, or AUTO for address summary). Resolves via system DNS (no shell). Loopback/link-local and unqualified internal names (localhost, *.local, *.internal, single-label) are refused unless HERD_ALLOW_INTERNAL_DNS=1.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Hostname, domain, or IP address (for PTR reverse lookups)",
				},
				"record_type": map[string]any{
					"type":        "string",
					"description": "Record type: A, AAAA, MX, TXT, NS, CNAME, PTR, SRV, or AUTO (default AUTO — resolved IP addresses)",
				},
				"service": map[string]any{
					"type":        "string",
					"description": "SRV service label without underscores (e.g. 'http' for _http._tcp.<name>). SRV only.",
				},
				"proto": map[string]any{
					"type":        "string",
					"description": "SRV protocol label without underscores, usually 'tcp' or 'udp'. SRV only.",
				},
			},
			"required": []string{"name"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			name, _ := args["name"].(string)
			name = strings.TrimSpace(strings.TrimSuffix(name, "."))
			if name == "" {
				return nil, fmt.Errorf("'name' parameter is required")
			}
			if len(name) > 253 {
				return nil, fmt.Errorf("name is too long")
			}
			if err := validateDNSLookupName(name); err != nil {
				return nil, err
			}

			recordType := "AUTO"
			if rt, ok := args["record_type"].(string); ok && strings.TrimSpace(rt) != "" {
				recordType = strings.ToUpper(strings.TrimSpace(rt))
			}

			srvService, _ := args["service"].(string)
			srvProto, _ := args["proto"].(string)
			srvService = strings.Trim(strings.TrimSpace(srvService), "_")
			srvProto = strings.Trim(strings.TrimSpace(srvProto), "_")
			if recordType == "SRV" {
				if (srvService == "") != (srvProto == "") {
					return nil, fmt.Errorf("SRV requires both 'service' and 'proto' (e.g. service http proto tcp)")
				}
				if srvService == "" {
					return nil, fmt.Errorf("SRV requires 'service' and 'proto' (e.g. service http proto tcp for _http._tcp.%s)", name)
				}
			} else if srvService != "" || srvProto != "" {
				return nil, fmt.Errorf("'service'/'proto' apply to SRV lookups only")
			}

			resolver := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					d := net.Dialer{Timeout: 5 * time.Second}
					return d.DialContext(ctx, network, address)
				},
			}

			return executeDNSLookup(ctx, resolver, name, recordType, srvService, srvProto)
		},
	}
}

// internalNameBlocked reports whether name targets loopback/link-local
// infrastructure or unqualified internal names. The model is
// attacker-influenced, so dns_lookup refuses these unless
// HERD_ALLOW_INTERNAL_DNS=1 (see docs/security-deep-dive.md).
func internalNameBlocked(name string) bool {
	if os.Getenv("HERD_ALLOW_INTERNAL_DNS") != "" {
		return false
	}
	lower := strings.ToLower(strings.TrimSuffix(name, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") ||
		lower == "local" || strings.HasSuffix(lower, ".local") ||
		strings.HasSuffix(lower, ".internal") || strings.HasSuffix(lower, ".lan") ||
		strings.HasSuffix(lower, ".home.arpa") || !strings.Contains(lower, ".") {
		return true
	}
	if ip := net.ParseIP(lower); ip != nil {
		return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsMulticast() || ip.IsUnspecified()
	}
	return false
}

func validateDNSLookupName(name string) error {
	// net.Resolver never invokes a shell, so this is defense-in-depth plus
	// an SSRF boundary for model-chosen names (see internalNameBlocked).
	if strings.ContainsAny(name, " \t\r\n/\\") {
		return fmt.Errorf("invalid name: contains disallowed characters")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid name: contains control characters")
		}
	}
	if strings.ContainsAny(name, ";$&|`'\"!") {
		return fmt.Errorf("invalid name: contains shell metacharacters")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return fmt.Errorf("invalid name: empty DNS label")
		}
		if len(label) > 63 {
			return fmt.Errorf("invalid name: DNS label too long")
		}
	}
	if internalNameBlocked(name) {
		return fmt.Errorf("refusing internal name %q (set HERD_ALLOW_INTERNAL_DNS=1 to override)", name)
	}
	return nil
}

func executeDNSLookup(ctx context.Context, r *net.Resolver, name, recordType, srvService, srvProto string) (map[string]any, error) {
	out := map[string]any{
		"name":         name,
		"record_type":  recordType,
		"records":      []any{},
		"record_count": 0,
	}

	switch recordType {
	case "AUTO", "A", "AAAA":
		return lookupIPRecords(ctx, r, name, recordType, out)

	case "MX":
		return lookupMXRecords(ctx, r, name, out)

	case "TXT":
		return lookupTXTRecords(ctx, r, name, out)

	case "NS":
		return lookupNSRecords(ctx, r, name, out)

	case "CNAME":
		return lookupCNAMERecord(ctx, r, name, out)

	case "PTR":
		return lookupPTRRecords(ctx, r, name, out)

	case "SRV":
		return lookupSRVRecords(ctx, r, name, srvService, srvProto, out)

	default:
		return nil, fmt.Errorf("unsupported record_type %q (use A, AAAA, MX, TXT, NS, CNAME, PTR, SRV, or AUTO)", recordType)
	}
}

func lookupIPRecords(ctx context.Context, r *net.Resolver, name, recordType string, out map[string]any) (map[string]any, error) {
	ips, err := r.LookupIPAddr(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed: %w", err)
	}
	var records []map[string]any
	for _, ipa := range ips {
		family := "A"
		if ipa.IP.To4() == nil {
			family = "AAAA"
		}
		if recordType == "A" && family != "A" {
			continue
		}
		if recordType == "AAAA" && family != "AAAA" {
			continue
		}
		records = append(records, map[string]any{
			"type":  family,
			"value": ipa.IP.String(),
		})
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no address records found for %q", name)
	}
	out["records"] = records
	out["record_count"] = len(records)
	return out, nil
}

func lookupMXRecords(ctx context.Context, r *net.Resolver, name string, out map[string]any) (map[string]any, error) {
	mxs, err := r.LookupMX(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed: %w", err)
	}
	var records []map[string]any
	for _, mx := range mxs {
		records = append(records, map[string]any{
			"type":     "MX",
			"host":     strings.TrimSuffix(mx.Host, "."),
			"priority": mx.Pref,
		})
	}
	out["records"] = records
	out["record_count"] = len(records)
	return out, nil
}

func lookupTXTRecords(ctx context.Context, r *net.Resolver, name string, out map[string]any) (map[string]any, error) {
	txts, err := r.LookupTXT(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed: %w", err)
	}
	var records []any
	for _, txt := range txts {
		records = append(records, map[string]any{"type": "TXT", "value": txt})
	}
	out["records"] = records
	out["record_count"] = len(records)
	return out, nil
}

func lookupNSRecords(ctx context.Context, r *net.Resolver, name string, out map[string]any) (map[string]any, error) {
	nss, err := r.LookupNS(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed: %w", err)
	}
	var records []any
	for _, ns := range nss {
		records = append(records, map[string]any{"type": "NS", "value": strings.TrimSuffix(ns.Host, ".")})
	}
	out["records"] = records
	out["record_count"] = len(records)
	return out, nil
}

func lookupCNAMERecord(ctx context.Context, r *net.Resolver, name string, out map[string]any) (map[string]any, error) {
	cname, err := r.LookupCNAME(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed: %w", err)
	}
	cname = strings.TrimSuffix(cname, ".")
	// LookupCNAME never fails: it returns the input when no alias
	// exists. Report that honestly instead of a phantom record.
	if strings.EqualFold(cname, strings.TrimSuffix(name, ".")) {
		return nil, fmt.Errorf("no CNAME record found for %q", name)
	}
	out["records"] = []any{map[string]any{"type": "CNAME", "value": cname}}
	out["record_count"] = 1
	return out, nil
}

func lookupPTRRecords(ctx context.Context, r *net.Resolver, name string, out map[string]any) (map[string]any, error) {
	addrs, err := r.LookupAddr(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed: %w", err)
	}
	var records []any
	for _, addr := range addrs {
		records = append(records, map[string]any{"type": "PTR", "value": strings.TrimSuffix(addr, ".")})
	}
	out["records"] = records
	out["record_count"] = len(records)
	return out, nil
}

func lookupSRVRecords(ctx context.Context, r *net.Resolver, name, service, proto string, out map[string]any) (map[string]any, error) {
	_, addrs, err := r.LookupSRV(ctx, service, proto, name)
	if err != nil {
		return nil, fmt.Errorf("dns lookup failed: %w", err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no SRV records found for _%s._%s.%s", service, proto, name)
	}
	var records []any
	for _, srv := range addrs {
		records = append(records, map[string]any{
			"type":     "SRV",
			"target":   strings.TrimSuffix(srv.Target, "."),
			"port":     srv.Port,
			"priority": srv.Priority,
			"weight":   srv.Weight,
		})
	}
	out["records"] = records
	out["record_count"] = len(records)
	return out, nil
}
