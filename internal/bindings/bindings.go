// Package bindings resolves the project and user roots so every harness
// computes the same memory location.
package bindings

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Resolved is the bound identity of one project.
type Resolved struct {
	ProjectRoot string `json:"projectRoot"`
	ProjectDir  string `json:"projectDir"`
	UserDir     string `json:"userDir"`
}

// DefaultUserDir mirrors the OpenCode adapter's computeUserRoot. The lookup
// distinguishes an unset variable from one set to the empty string, as the
// TypeScript helper does.
func DefaultUserDir(goos string, lookup func(string) (string, bool)) string {
	if goos == "windows" {
		local, ok := lookup("LOCALAPPDATA")
		if !ok {
			if profile, ok := lookup("USERPROFILE"); ok {
				local = windowsJoin(profile, "AppData", "Local")
			} else {
				local = `C:\Users\default\AppData\Local`
			}
		}
		return windowsJoin(local, "recollect")
	}
	home, ok := lookup("HOME")
	if !ok {
		home = "~"
	}
	return path.Join(home, ".recollect")
}

// windowsJoin joins Windows path elements and preserves a UNC prefix.
func windowsJoin(elem ...string) string {
	parts := make([]string, 0, len(elem))
	unc := false
	for i, part := range elem {
		part = strings.ReplaceAll(part, "/", `\`)
		if i == 0 && strings.HasPrefix(part, `\\`) {
			unc = true
			part = strings.TrimLeft(part, `\`)
		}
		part = strings.Trim(part, `\`)
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	joined := strings.Join(parts, `\`)
	if unc {
		return `\\` + joined
	}
	return joined
}

// ProjectID is the 16-character hash the OpenCode adapter uses for a canonical
// root.
func ProjectID(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:16]
}

// Resolve binds a project hint and an optional user dir. An empty user dir
// uses the host default.
func Resolve(projectHint, userDir string) (Resolved, error) {
	root, err := CanonicalRoot(projectHint)
	if err != nil {
		return Resolved{}, err
	}
	if strings.TrimSpace(userDir) == "" {
		userDir = DefaultUserDir(runtime.GOOS, os.LookupEnv)
	}
	return Resolved{
		ProjectRoot: root,
		ProjectDir:  filepath.Join(userDir, "projects", ProjectID(root)),
		UserDir:     userDir,
	}, nil
}

// CanonicalRoot resolves symlinks and returns the repository root. A linked
// git worktree resolves to its main checkout, matching OpenCode's
// project.canonical.
func CanonicalRoot(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	resolved := absolute
	if evaluated, evalErr := filepath.EvalSymlinks(absolute); evalErr == nil {
		resolved = evaluated
	}
	root, ok := findVCSRoot(resolved)
	if !ok {
		return resolved, nil
	}
	return root, nil
}

func findVCSRoot(start string) (string, bool) {
	for dir := start; ; dir = filepath.Dir(dir) {
		for _, marker := range []string{".git", ".hg"} {
			if _, err := os.Lstat(filepath.Join(dir, marker)); err != nil {
				continue
			}
			if marker == ".hg" {
				return dir, true
			}
			return gitCanonical(dir), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
	}
}

func gitCanonical(dir string) string {
	gitDir, ok := gitOutput(dir, "rev-parse", "--path-format=absolute", "--git-dir")
	if !ok {
		return fallbackCanonical(dir)
	}
	commonDir, ok := gitOutput(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if !ok {
		return fallbackCanonical(dir)
	}
	if gitDir == commonDir {
		if top, ok := gitOutput(dir, "rev-parse", "--show-toplevel"); ok {
			return nativePath(top)
		}
		return dir
	}
	if main, ok := firstWorktree(dir); ok {
		return nativePath(main)
	}
	return fallbackCanonical(dir)
}

// nativePath normalizes a git-returned path to the host's separator and
// resolves symlinks. Git for Windows prints forward slashes, while OpenCode
// hashes the native, symlink-resolved form.
func nativePath(value string) string {
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if evaluated, err := filepath.EvalSymlinks(cleaned); err == nil {
		return evaluated
	}
	return cleaned
}

func fallbackCanonical(dir string) string {
	if main, ok := linkedWorktreeMain(dir); ok {
		return nativePath(main)
	}
	return dir
}

func gitOutput(dir string, args ...string) (string, bool) {
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := command.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func firstWorktree(dir string) (string, bool) {
	out, ok := gitOutput(dir, "worktree", "list", "--porcelain")
	if !ok {
		return "", false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			return strings.TrimPrefix(line, "worktree "), true
		}
	}
	return "", false
}

// linkedWorktreeMain reads the .git file when git is unavailable. The standard
// layout is <main>/.git/worktrees/<name>.
func linkedWorktreeMain(dir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	gitDir = filepath.Clean(gitDir)
	if filepath.Base(filepath.Dir(gitDir)) != "worktrees" {
		return "", false
	}
	common := filepath.Dir(filepath.Dir(gitDir))
	if filepath.Base(common) != ".git" {
		return "", false
	}
	return filepath.Dir(common), true
}
