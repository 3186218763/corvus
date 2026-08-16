package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"

	fileencoding "corvus/internal/fileutil/encoding"
)

type dotEnvFile struct {
	Path   string
	Values map[string]string
}

// loadDotEnv loads Corvus's global .env for provider credentials. The
// workspace .env values returned by loadDotEnvForRoot are ignored here because
// loadDotEnv has no Config to carry a workspace-scoped expansion environment.
func loadDotEnv() {
	loadDotEnvForRoot(".")
}

// loadDotEnvForRoot returns workspace .env values for scoped plugin/MCP/proxy
// expansion, then loads Corvus's global .env for provider credentials.
// Workspace .env values are deliberately not written into the process
// environment, so multiple desktop/ACP workspaces cannot leak tokens into each
// other and project files cannot redirect Corvus's own config/credential
// paths.
func loadDotEnvForRoot(root string) map[string]string {
	projectEnv := loadProjectDotEnvForExpansion(root)
	loadCredentialStoreForRoot(root)
	return projectEnv
}

func loadProjectDotEnvForExpansion(root string) map[string]string {
	root = resolveRoot(root)
	path := ".env"
	if root != "." {
		path = filepath.Join(root, ".env")
	}
	if current := UserCredentialsPath(); current != "" && samePath(path, current) {
		return nil
	}
	file, ok := readDotEnvFile(path)
	if !ok {
		return nil
	}
	return file.filtered(func(key string) bool {
		return !isProjectDotEnvControlKey(key)
	})
}

func isProjectDotEnvControlKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return true
	}
	upper := strings.ToUpper(key)
	if strings.HasPrefix(upper, "CORVUS_") {
		return true
	}
	switch upper {
	case "HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME":
		return true
	default:
		return false
	}
}

func loadDotEnvFileAs(path string, source CredentialSource) {
	file, ok := readDotEnvFile(path)
	if !ok {
		return
	}
	for key, val := range file.Values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists && source.Kind != CredentialSourceCredentials {
			recordExistingCredentialSource(key)
			continue
		}
		if err := os.Setenv(key, val); err == nil && source.Kind != "" {
			source.Path = path
			recordCredentialSource(key, val, source)
		}
	}
}

func readDotEnvFile(path string) (dotEnvFile, bool) {
	raw, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return dotEnvFile{}, false
	}
	values, err := godotenv.Unmarshal(string(raw))
	if err != nil {
		return dotEnvFile{}, false
	}
	return dotEnvFile{
		Path:   path,
		Values: values,
	}, true
}

func (f dotEnvFile) filtered(allow func(string) bool) map[string]string {
	out := map[string]string{}
	for key, val := range f.Values {
		key = strings.TrimSpace(key)
		if key == "" || allow != nil && !allow(key) {
			continue
		}
		out[key] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func envFileValue(path, wantKey string) (string, bool) {
	file, ok := readDotEnvFile(path)
	if !ok {
		return "", false
	}
	val, ok := file.Values[wantKey]
	return val, ok
}
