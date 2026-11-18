package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ProjectMeta is the project.json payload: which canonical root owns this
// directory, recorded once on first use so /memory and debugging can map a
// key back to a path.
type ProjectMeta struct {
	Root      string    `json:"root"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"createdAt"`
}

// projectMarkerNames pin a directory as a shared project root for memory,
// even when several nested git repositories live below it. The xi name is
// primary; the opencode-mem name is accepted for compatibility with that
// tool's marker.
var projectMarkerNames = []string{".xi-project", ".opencode-mem-project"}

// findMarkerProjectRoot returns the first ancestor of directory (including
// directory) that contains a project marker, or "" when none is found.
func findMarkerProjectRoot(directory string) string {
	dir := directory
	for {
		for _, name := range projectMarkerNames {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// DetectProjectRoot resolves the project boundary for cwd: the nearest
// ancestor (including cwd) containing a .git entry, else the resolved cwd.
// A .xi-project marker takes precedence over git detection so nested repos
// can share one store. Symlinks are resolved first so the same directory
// reached two ways maps to one project.
func DetectProjectRoot(cwd string) (string, error) {
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		cwd = wd
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		resolved = abs
	}
	if marker := findMarkerProjectRoot(resolved); marker != "" {
		return marker, nil
	}
	dir := resolved
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return resolved, nil
		}
		dir = parent
	}
}

// ProjectKey returns the stable "<slug>-<hash8>" directory name for a
// canonical project root.
func ProjectKey(root string) string {
	return slugify(filepath.Base(root)) + "-" + hash8(root)
}

// ProjectDir is the per-project memory directory under the agent dir.
func ProjectDir(agentDir, root string) string {
	return ProjectDirForKey(agentDir, ProjectKey(root))
}

// ProjectDirForKey is the memory directory for a workspace key:
// ProjectKey(root) or an explicit, path-safe workspace id.
func ProjectDirForKey(agentDir, key string) string {
	return filepath.Join(MemoryRoot(agentDir), projectsDirName, key)
}

// ProjectMetaFor builds the identity record for root. CreatedAt stays zero
// until the store stamps it from its injected clock.
func ProjectMetaFor(root string) ProjectMeta {
	return ProjectMeta{
		Root: root,
		Name: filepath.Base(root),
		Slug: slugify(filepath.Base(root)),
	}
}

// slugify lowercases name and keeps [a-z0-9] runs joined by single dashes,
// capped at 24 characters. Empty results become "project".
func slugify(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 24 {
		s = strings.Trim(s[:24], "-")
	}
	if s == "" {
		return "project"
	}
	return s
}

// hash8 is the first 8 hex characters of sha256(root). Windows and macOS
// fold case before hashing so a case-varied path cannot mint a second key on
// a case-insensitive filesystem.
func hash8(root string) string {
	key := root
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		key = strings.ToLower(key)
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:8]
}
