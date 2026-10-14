package agent

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// buildDNSLookupTool returns an agent tool for resolving DNS records.
func buildDNSLookupTool() ToolDefinition {
	return ToolDefinition{
		Name:        "dns_lookup",
		Description: "Look up DNS records for a hostname or IP address (A, AAAA, MX, TXT, NS, CNAME, PTR, SRV, or AUTO for address summary).",
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

			resolver := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					d := net.Dialer{Timeout: 5 * time.Second}
					return d.DialContext(ctx, network, address)
				},
			}

			return executeDNSLookup(ctx, resolver, name, recordType)
		},
	}
}

func validateDNSLookupName(name string) error {
	if strings.ContainsAny(name, " \t\r\n/\\") {
		return fmt.Errorf("invalid name: contains disallowed characters")
	}
	return nil
}

func executeDNSLookup(ctx context.Context, r *net.Resolver, name, recordType string) (map[string]any, error) {
	out := map[string]any{
		"name":         name,
		"record_type":  recordType,
		"records":      []any{},
		"record_count": 0,
	}

	switch recordType {
	case "AUTO", "A", "AAAA":
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
		if len(records) == 0 && recordType != "AUTO" {
			return nil, fmt.Errorf("no %s records found for %q", recordType, name)
		}
		out["records"] = records
		out["record_count"] = len(records)
		return out, nil

	case "MX":
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

	case "TXT":
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

	case "NS":
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

	case "CNAME":
		cname, err := r.LookupCNAME(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("dns lookup failed: %w", err)
		}
		cname = strings.TrimSuffix(cname, ".")
		out["records"] = []any{map[string]any{"type": "CNAME", "value": cname}}
		out["record_count"] = 1
		return out, nil

	case "PTR":
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

	case "SRV":
		_, addrs, err := r.LookupSRV(ctx, "", "", name)
		if err != nil {
			return nil, fmt.Errorf("dns lookup failed: %w", err)
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

	default:
		return nil, fmt.Errorf("unsupported record_type %q (use A, AAAA, MX, TXT, NS, CNAME, PTR, SRV, or AUTO)", recordType)
	}
}
