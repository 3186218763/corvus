package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"corvus/internal/fileutil"
)

// ProjectTrustDecision is the result of the one decision that must happen
// before a repository-controlled settings file is parsed. Once means that the
// current load may use the hooks; Remember also records the canonical project
// root for later loads.
type ProjectTrustDecision string

const (
	ProjectTrustDeny     ProjectTrustDecision = "deny"
	ProjectTrustOnce     ProjectTrustDecision = "once"
	ProjectTrustRemember ProjectTrustDecision = "remember"
)

// ProjectTrustRequest describes the project whose hook settings are about to
// be loaded. The request intentionally contains paths only; callers must make
// the trust decision without executing or parsing project code.
type ProjectTrustRequest struct {
	ProjectRoot  string
	SettingsPath string
}

// TrustOutcome is a small audit vocabulary for the load-time trust gate.
type TrustOutcome string

const (
	TrustOutcomeExisting    TrustOutcome = "existing"
	TrustOutcomeOnce        TrustOutcome = "once"
	TrustOutcomeRemember    TrustOutcome = "remember"
	TrustOutcomeDenied      TrustOutcome = "denied"
	TrustOutcomeHeadless    TrustOutcome = "headless-denied"
	TrustOutcomeUnavailable TrustOutcome = "decision-unavailable"
)

// TrustAudit describes a project-hook load decision. It is deliberately
// separate from hook execution audit records: this decision happens before a
// project settings file is parsed and may be the only evidence that hooks were
// suppressed.
type TrustAudit struct {
	ProjectRoot  string
	SettingsPath string
	Outcome      TrustOutcome
	Persisted    bool
}

// LoadReport reports the project-hook part of Load without exposing any
// project settings contents. Global and installed-plugin hooks are unaffected
// by this gate.
type LoadReport struct {
	ProjectRoot         string
	ProjectSettingsPath string
	ProjectHooksFound   bool
	ProjectHooksEnabled bool
	TrustRequired       bool
	TrustDenied         bool
	TrustPersisted      bool
}

// ProjectTrustFilename is the user-state file containing canonical project
// roots explicitly trusted for project hooks. It lives beside global Corvus
// settings, never inside the repository being trusted.
const ProjectTrustFilename = "project-hook-trust.json"

type projectTrustFile struct {
	Version  int      `json:"version"`
	Projects []string `json:"projects"`
}

var projectTrustMu sync.Mutex

// ProjectTrustPath returns the user-state path used by the project-hook trust
// store. homeDir has the same test/legacy meaning as LoadOptions.HomeDir.
func ProjectTrustPath(homeDir string) string {
	return filepath.Join(corvusHome(homeDir), ProjectTrustFilename)
}

// NormalizeProjectRoot resolves an existing root through symlinks and falls
// back to its absolute cleaned spelling when the directory has disappeared.
// A moved directory therefore requires a new decision, while alternate
// symlink spellings refer to the same trust record.
func NormalizeProjectRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("project root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("absolute project root: %w", err)
	}
	abs = filepath.Clean(abs)
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = filepath.Clean(real)
	}
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs, nil
}

func projectTrustKey(root string) string {
	key, err := NormalizeProjectRoot(root)
	if err != nil {
		return ""
	}
	return key
}

// IsProjectTrusted reports the durable trust decision for root. A malformed
// or unreadable trust file fails closed and returns false.
func IsProjectTrusted(homeDir, root string) bool {
	key := projectTrustKey(root)
	if key == "" {
		return false
	}
	projectTrustMu.Lock()
	defer projectTrustMu.Unlock()
	projects, err := readProjectTrustLocked(homeDir)
	if err != nil {
		return false
	}
	_, ok := projects[key]
	return ok
}

// TrustProject records a durable trust decision for root. It does not parse
// or execute the project's settings.
func TrustProject(homeDir, root string) error {
	return updateProjectTrust(homeDir, root, true)
}

// RevokeProjectTrust removes the durable decision for root. A subsequent load
// will again suppress project hooks until the user explicitly trusts the root.
func RevokeProjectTrust(homeDir, root string) error {
	return updateProjectTrust(homeDir, root, false)
}

func updateProjectTrust(homeDir, root string, trusted bool) error {
	key := projectTrustKey(root)
	if key == "" {
		return fmt.Errorf("cannot update trust for empty project root")
	}
	path := ProjectTrustPath(homeDir)
	if strings.TrimSpace(path) == "" || strings.TrimSpace(filepath.Dir(path)) == "." {
		return fmt.Errorf("project hook trust path is unavailable")
	}

	projectTrustMu.Lock()
	defer projectTrustMu.Unlock()
	projects, err := readProjectTrustLocked(homeDir)
	if err != nil {
		return err
	}
	if trusted {
		projects[key] = struct{}{}
	} else {
		delete(projects, key)
	}
	return writeProjectTrustLocked(path, projects)
}

func readProjectTrustLocked(homeDir string) (map[string]struct{}, error) {
	path := ProjectTrustPath(homeDir)
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]struct{}{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read project hook trust: %w", err)
	}
	var file projectTrustFile
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, fmt.Errorf("decode project hook trust: %w", err)
	}
	projects := make(map[string]struct{}, len(file.Projects))
	for _, root := range file.Projects {
		if key := projectTrustKey(root); key != "" {
			projects[key] = struct{}{}
		}
	}
	return projects, nil
}

func writeProjectTrustLocked(path string, projects map[string]struct{}) error {
	roots := make([]string, 0, len(projects))
	for root := range projects {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	body, err := json.MarshalIndent(projectTrustFile{Version: 1, Projects: roots}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project hook trust: %w", err)
	}
	body = append(body, '\n')
	if err := fileutil.AtomicWriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("write project hook trust: %w", err)
	}
	return nil
}
