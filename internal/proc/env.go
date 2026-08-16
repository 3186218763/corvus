package proc

import (
	"runtime"
	"strings"
)

// ParseShellPATH scans a shell PATH probe's combined output backwards for the
// last line carrying marker and returns the trimmed remainder.
func ParseShellPATH(out []byte, marker string) string {
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], marker) {
			return strings.TrimSpace(strings.TrimPrefix(lines[i], marker))
		}
	}
	return ""
}

// SetEnvValue returns env with the key=value pair replaced in place (last
// occurrence wins) or appended when absent.
func SetEnvValue(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if ok && EnvKeyEqual(k, key) {
			if !replaced {
				out = append(out, key+"="+value)
				replaced = true
			}
			continue
		}
		out = append(out, kv)
	}
	if !replaced {
		out = append(out, key+"="+value)
	}
	return out
}

// EnvValue returns the value of the last key= entry in env.
func EnvValue(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(env[i], "=")
		if ok && EnvKeyEqual(k, key) {
			return v, true
		}
	}
	return "", false
}

// EnvKeyEqual compares env keys case-insensitively on Windows.
func EnvKeyEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
