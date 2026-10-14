package cli

import (
	"flag"
	"strings"
)

// cliBoolFlags lists flags that never take a separate value argument.
var cliBoolFlags = map[string]bool{
	"json":   true,
	"wait":   true,
	"ack":    true,
	"force":  true,
	"global": true,
	"all":    true,
	"h":      true,
	"help":   true,
}

// cliValueFlags lists flags that take the next token as a value (when not using = syntax).
var cliValueFlags = map[string]bool{
	"node-name": true,
	"socket":    true,
	"ttl":       true,
	"timeout":   true,
	"bind":      true,
	"prefix":    true,
	"topic":     true,
	"thread":    true,
	"target":    true,
	"dir":       true,
	"model":     true,
	"role":      true,
	"export":    true,
	"interval":  true,
}

// ReorderCLIArgs moves flags before positional arguments so users can put --socket and
// other options after command-specific positionals (standard flag.Parse stops at the first non-flag).
func ReorderCLIArgs(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i:]...)
			break
		}
		name, _, isFlagArg := parseCLIArg(a)
		if !isFlagArg {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		if strings.Contains(a, "=") {
			continue
		}
		if cliValueFlags[name] && !cliBoolFlags[name] {
			if i+1 < len(args) && !looksLikeFlag(args[i+1]) {
				flags = append(flags, args[i+1])
				i++
			}
		}
	}
	return append(flags, pos...)
}

func parseCLIArg(arg string) (name string, inlineValue string, isFlag bool) {
	if !looksLikeFlag(arg) {
		return "", "", false
	}
	if strings.HasPrefix(arg, "--") {
		body := strings.TrimPrefix(arg, "--")
		if idx := strings.Index(body, "="); idx >= 0 {
			return body[:idx], body[idx+1:], true
		}
		return body, "", true
	}
	// Single-dash flags (-socket, -timeout, -json)
	name = strings.TrimPrefix(arg, "-")
	return name, "", true
}

func looksLikeFlag(s string) bool {
	return strings.HasPrefix(s, "-") && s != "-"
}

// ParseFlags parses args after ReorderCLIArgs so flags may appear after positionals.
func ParseFlags(fs *flag.FlagSet, args []string) error {
	return fs.Parse(ReorderCLIArgs(args))
}
