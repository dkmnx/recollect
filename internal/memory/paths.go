package memory

import (
	"path/filepath"
	"regexp"
	"strings"
)

const (
	userFileName     = "user.md"
	indexFileName    = "MEMORY.md"
	topicsDirName    = "topics"
	pagesDirName     = "pages"
	archiveDirName   = "archive"
	projectsDirName  = "projects"
	metaFileName     = "project.json"
	markdownExt      = ".md"
	sessionsDirName  = "sessions"
	digestFileExt    = ".md"
	captureStateFile = "capture.json"
	// captureAttemptFile holds the diagnostics of the most recent capture
	// phase. It is a diagnostic, not state: a missing or unreadable file
	// costs no fact and no marker.
	captureAttemptFile = "capture_attempt.json"
	// journalFile is the append-only change log; journalPrevFile holds the
	// previous generation after rotation.
	journalFile     = "journal.jsonl"
	journalPrevFile = "journal.prev.jsonl"
	// chainFile is the append-only revision chain; chainPrevFile holds the
	// previous generation after rotation.
	chainFile     = "chains.jsonl"
	chainPrevFile = "chains.prev.jsonl"
)

// Legacy file names from the pre-format layout. Only the migrator reads
// them; the store never writes them.
const (
	legacyCorrectionsFile = "corrections.md"
	legacySummaryFile     = ".summary.md"
	legacyTopicExt        = ".md"
)

var topicNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidTopicName reports whether name is a legal topic directory name.
func ValidTopicName(name string) bool { return topicNameRE.MatchString(name) }

// containPath joins an untrusted name onto a memory root and refuses
// escapes: absolute paths and any `..` segment (POSIX or Windows style).
// The final relative check holds even if the name smuggles `.` segments or
// redundant separators past the scan. Every dynamic store join (fact files,
// digest files) goes through here; fixed-name paths need no check.
func containPath(root, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", Errorf(CodePathEscape, "empty memory path")
	}
	if filepath.IsAbs(name) {
		return "", Errorf(CodePathEscape, "absolute memory path")
	}
	for _, segment := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == ".." {
			return "", Errorf(CodePathEscape, "memory path escapes its root")
		}
	}
	joined := filepath.Join(root, name)
	rel, err := filepath.Rel(root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", Errorf(CodePathEscape, "memory path escapes its root")
	}
	return joined, nil
}

// MemoryRoot is the parent of user.md and projects/ under the agent dir.
func MemoryRoot(agentDir string) string { return filepath.Join(agentDir, "memory") }

// UserFile is the global user memory file inside the memory root.
func UserFile(memoryRoot string) string { return filepath.Join(memoryRoot, userFileName) }

// IndexFile is the project index (MEMORY.md) path.
func IndexFile(projectDir string) string { return filepath.Join(projectDir, indexFileName) }

// TopicsDir is the project topic directory.
func TopicsDir(projectDir string) string { return filepath.Join(projectDir, topicsDirName) }

// PagesDir holds the derived knowledge pages. Engine state outside format
// v1: external readers must ignore the directory.
func PagesDir(projectDir string) string { return filepath.Join(projectDir, pagesDirName) }

// ArchiveDir is the project snapshot directory.
func ArchiveDir(projectDir string) string { return filepath.Join(projectDir, archiveDirName) }

// ProjectMetaFile is the project identity record written once per project.
func ProjectMetaFile(projectDir string) string { return filepath.Join(projectDir, metaFileName) }

// SessionsDir is the per-session digest directory.
func SessionsDir(projectDir string) string { return filepath.Join(projectDir, sessionsDirName) }

// ArchiveSessionsDir holds digests that aged out of the searchable set.
// Archived files stay readable by path and are never searched.
func ArchiveSessionsDir(projectDir string) string {
	return filepath.Join(ArchiveDir(projectDir), sessionsDirName)
}

// CaptureStateFile is the persisted capture-marker file.
func CaptureStateFile(projectDir string) string {
	return filepath.Join(projectDir, captureStateFile)
}

// CaptureAttemptFile is the persisted last-capture-phase record.
func CaptureAttemptFile(projectDir string) string {
	return filepath.Join(projectDir, captureAttemptFile)
}

// JournalFile is the project change journal path.
func JournalFile(projectDir string) string {
	return filepath.Join(projectDir, journalFile)
}

// ChainFile is the project revision chain path.
func ChainFile(projectDir string) string {
	return filepath.Join(projectDir, chainFile)
}
