package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"corvus/internal/config"
	"corvus/internal/secrets"
)

func resolveWorkspaceRoot(explicit string) string {
	if explicit != "" {
		return explicit
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if root, ok := nearestGitRoot(wd); ok {
		return root
	}
	return wd
}

func normalizeAdditionalDirs(root string, dirs []string) ([]string, error) {
	if len(dirs) == 0 {
		return nil, nil
	}
	base := strings.TrimSpace(root)
	if base == "" {
		base = "."
	}
	if !filepath.IsAbs(base) {
		abs, err := filepath.Abs(base)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace root: %w", err)
		}
		base = abs
	}

	var out []string
	for _, raw := range dirs {
		dir := strings.TrimSpace(raw)
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(base, dir)
		}
		dir, err := filepath.Abs(filepath.Clean(dir))
		if err != nil {
			return nil, fmt.Errorf("resolve additional directory %q: %w", raw, err)
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return nil, fmt.Errorf("resolve additional directory %q: %w", raw, err)
		}
		info, err := os.Stat(real)
		if err != nil {
			return nil, fmt.Errorf("inspect additional directory %q: %w", raw, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("additional path %q is not a directory", raw)
		}
		out = appendUniquePaths(out, filepath.Clean(real))
	}
	return out, nil
}

func appendUniquePaths(base []string, extra ...string) []string {
	out := append([]string(nil), base...)
	seen := make(map[string]struct{}, len(out)+len(extra))
	for _, path := range out {
		seen[pathComparisonKey(path)] = struct{}{}
	}
	for _, path := range extra {
		path = filepath.Clean(path)
		key := pathComparisonKey(path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, path)
	}
	return out
}

// RuntimeForbidReadRoots returns the configured deny roots plus Corvus's
// global credential FILE when it exists. It also registers the corresponding
// credential environment names for subprocess filtering. Runtime tool
// assemblers outside Build must use this helper instead of reading the config
// roots directly.
//
// Provider and bot credentials are loaded into the parent process from this
// file, so readers, shell commands, and MCP servers must not be able to recover
// them even when the optional broad sensitive-file denylist is off. Project
// .env files retain their existing behavior.

func RuntimeForbidReadRoots(cfg *config.Config, root string) []string {
	if cfg == nil {
		return nil
	}
	secrets.RegisterCredentialEnvKeys(cfg.CredentialEnvNames())
	base := cfg.ForbidReadRootsForRoot(root)
	credentialPath := strings.TrimSpace(config.UserCredentialsPath())
	if credentialPath == "" {
		return append([]string(nil), base...)
	}
	info, err := os.Stat(credentialPath)
	if err != nil || info.IsDir() {
		return append([]string(nil), base...)
	}
	if real, err := filepath.EvalSymlinks(credentialPath); err == nil {
		credentialPath = real
	}
	return appendUniquePaths(base, credentialPath)
}

func pathComparisonKey(path string) string {
	path = filepath.Clean(path)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func nearestGitRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		dir = filepath.Clean(start)
	}
	for {
		if isGitMarker(filepath.Join(dir, ".git")) {
			return dir, true
		}
		next := filepath.Dir(dir)
		if next == dir {
			return "", false
		}
		dir = next
	}
}

func isGitMarker(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
}
