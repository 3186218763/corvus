package builtin

import (
	"fmt"
	"strings"

	"corvus/internal/netpolicy"
	"corvus/internal/shellparse"
)

// denyNetworkEgressURLs checks a shell command for recognizable outbound-URL
// arguments and returns an error when one hits a policy deny rule. It is
// deliberately conservative:
//
//   - Only the command word's URL-taking forms are inspected: curl/wget (first
//     non-flag arguments) and the PowerShell web cmdlets (Invoke-WebRequest /
//     Invoke-RestMethod and their iwr/irm aliases, -Uri/-Url values plus a
//     first positional argument).
//   - Only single static commands parse (shellparse.StaticFields rejects
//     control operators, pipes, expansions, and here-docs); anything it cannot
//     parse is skipped, never guessed.
//   - An argument that does not yield a hostname is skipped, so ordinary text
//     arguments (headers, filenames, data) never reach the policy.
//
// The check is deny-rule-only: the policy Default is deliberately not
// consulted, because command parsing is lossy and must never block a command
// that merely looks like it might reach the network. The coarse [sandbox]
// network switch remains the enforcement layer for everything else.
func (b bash) denyNetworkEgressURLs(command string) error {
	if len(b.netPolicy.Allow) == 0 && len(b.netPolicy.Deny) == 0 {
		return nil
	}
	for _, candidate := range urlCandidates(command) {
		decision, rule := b.netPolicy.Decide(candidate)
		if decision == netpolicy.Deny && rule != "" {
			return fmt.Errorf("network policy denied %s in command: matched deny rule %q", candidate, rule)
		}
	}
	return nil
}

// urlCandidates extracts the URL arguments of a single static shell command.
// It returns nil when the command is not a recognized URL-taking form or
// cannot be parsed statically.
func urlCandidates(command string) []string {
	fields, malformed := shellparse.StaticFields(command)
	if malformed != "" || len(fields) == 0 {
		return nil
	}
	switch strings.ToLower(strings.TrimSuffix(fields[0], ".exe")) {
	case "curl", "wget":
		var out []string
		for i := 1; i < len(fields); i++ {
			arg := fields[i]
			if strings.HasPrefix(arg, "-") {
				// Value-taking flags whose value is a filename or local path,
				// never a URL: skip the value so it is not scanned.
				switch arg {
				case "-o", "--output", "--output-file", "-O", "--output-document", "-P", "--directory-prefix", "-T", "--upload-file":
					i++
				}
				continue
			}
			out = append(out, arg)
		}
		return out
	case "invoke-webrequest", "iwr", "invoke-restmethod", "irm":
		var out []string
		for i := 1; i < len(fields); i++ {
			arg := fields[i]
			if strings.EqualFold(arg, "-uri") || strings.EqualFold(arg, "-url") {
				if i+1 < len(fields) {
					out = append(out, fields[i+1])
				}
				i++
				continue
			}
			if strings.HasPrefix(arg, "-") {
				continue
			}
			// First non-flag positional argument: iwr/irm accept the URL
			// positionally (and the full cmdlets do too).
			if len(out) == 0 {
				out = append(out, arg)
			}
		}
		return out
	default:
		return nil
	}
}
