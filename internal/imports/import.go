// Package imports copies a read-only xi format-v1 memory store into a
// Recollect target. It never writes to the source.
package imports

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dkmnx/recollect/internal/fsatomic"
	"github.com/dkmnx/recollect/internal/memory"
)

// Source is a read-only xi memory store.
type Source struct {
	ProjectDir string
	UserDir    string
}

// Options configures one import run. Target supplies index regeneration;
// TargetDir and TargetUserDir are the directories that receive the files.
type Options struct {
	Source        Source
	Target        *memory.Store
	TargetDir     string
	TargetUserDir string
	DryRun        bool
}

// Mapping records one source-to-target path and the source hash it came from.
type Mapping struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	Hash string `json:"hash"`
}

// Report is the import result and the durable manifest contents.
type Report struct {
	SourceProjectDir string    `json:"sourceProjectDir"`
	SourceUserDir    string    `json:"sourceUserDir"`
	DryRun           bool      `json:"dryRun"`
	Mappings         []Mapping `json:"mappings"`
	Collisions       []string  `json:"collisions"`
	Omitted          []string  `json:"omitted"`
	Repairs          []string  `json:"repairs"`
	Applied          []string  `json:"applied"`
	Snapshot         string    `json:"snapshot"`
}

// fileItem is one planned byte-for-byte copy.
type fileItem struct {
	from    string
	to      string
	kind    string
	hash    string
	modTime time.Time
}

// copyPlan holds the items planned before any write.
type copyPlan struct {
	files     []fileItem
	indexFrom string
	indexTo   string
	indexHash string
	omitted   []string
}

const (
	kindFact    = "fact"
	kindUser    = "user"
	kindDigest  = "digest"
	kindPage    = "page"
	kindChain   = "chain"
	kindCapture = "capture"
	kindJournal = "journal"
	kindIndex   = "index"
)

// Run imports source into target and returns the full report. Dry run builds
// the same plan without writing any file or manifest.
func Run(opts Options) (Report, error) {
	report := Report{
		Mappings:   []Mapping{},
		Collisions: []string{},
		Omitted:    []string{},
		Repairs:    []string{},
		Applied:    []string{},
		Snapshot:   time.Now().UTC().Format(time.RFC3339Nano),
	}

	sourceProject, err := absoluteDir(opts.Source.ProjectDir)
	if err != nil {
		return report, fmt.Errorf("source project dir: %w", err)
	}
	sourceUser, err := absoluteDir(opts.Source.UserDir)
	if err != nil {
		return report, fmt.Errorf("source user dir: %w", err)
	}
	targetDir := opts.TargetDir
	if targetDir == "" && opts.Target != nil {
		targetDir = opts.Target.ProjectDir()
	}
	targetProject, err := absoluteDir(targetDir)
	if err != nil {
		return report, fmt.Errorf("target dir: %w", err)
	}
	targetUserDir := opts.TargetUserDir
	if targetUserDir == "" && opts.Target != nil {
		targetUserDir = filepath.Dir(opts.Target.UserPath())
	}
	targetUser, err := absoluteDir(targetUserDir)
	if err != nil {
		return report, fmt.Errorf("target user dir: %w", err)
	}
	report.SourceProjectDir = sourceProject
	report.SourceUserDir = sourceUser
	report.DryRun = opts.DryRun

	plan, err := buildPlan(sourceProject, sourceUser, targetProject, targetUser)
	if err != nil {
		return report, err
	}
	report.Omitted = append(report.Omitted, plan.omitted...)

	prior, err := readApplied(manifestPath(targetProject, sourceProject, sourceUser))
	if err != nil {
		return report, err
	}

	factsApplied := false
	for _, item := range plan.files {
		mapping := Mapping{From: item.from, To: item.to, Kind: item.kind, Hash: item.hash}
		report.Mappings = append(report.Mappings, mapping)
		status := decideCopy(item, prior)
		if status == decisionCollision {
			report.Collisions = append(report.Collisions, item.to)
			continue
		}
		if status == decisionSkip {
			continue
		}
		if opts.DryRun {
			continue
		}
		if err := writeItem(item); err != nil {
			return report, err
		}
		report.Applied = append(report.Applied, item.to)
		if item.kind == kindFact {
			factsApplied = true
		}
	}

	report.Mappings = append(report.Mappings, Mapping{
		From: plan.indexFrom, To: plan.indexTo, Kind: kindIndex, Hash: plan.indexHash,
	})
	if !opts.DryRun {
		_, statErr := os.Lstat(plan.indexTo)
		indexMissing := errors.Is(statErr, os.ErrNotExist)
		if statErr != nil && !indexMissing {
			return report, fmt.Errorf("stat target index: %w", statErr)
		}
		if factsApplied || indexMissing {
			if opts.Target == nil {
				return report, errors.New("target store is required to regenerate MEMORY.md")
			}
			if err := opts.Target.RegenerateIndex(); err != nil {
				return report, fmt.Errorf("regenerate index: %w", err)
			}
			report.Applied = append(report.Applied, plan.indexTo)
		}
	}

	sort.Slice(report.Mappings, func(i, j int) bool {
		if report.Mappings[i].Kind != report.Mappings[j].Kind {
			return report.Mappings[i].Kind < report.Mappings[j].Kind
		}
		if report.Mappings[i].From != report.Mappings[j].From {
			return report.Mappings[i].From < report.Mappings[j].From
		}
		return report.Mappings[i].To < report.Mappings[j].To
	})
	sort.Strings(report.Collisions)
	sort.Strings(report.Omitted)
	sort.Strings(report.Applied)

	if opts.DryRun {
		return report, nil
	}
	path := manifestPath(targetProject, sourceProject, sourceUser)
	if err := writeManifest(path, report); err != nil {
		return report, err
	}
	return report, nil
}

type copyDecision int

const (
	decisionApply copyDecision = iota
	decisionSkip
	decisionCollision
)

// decideCopy compares the source hash with the last applied hash and the
// current target bytes. A target changed outside import is never overwritten.
func decideCopy(item fileItem, prior map[string]string) copyDecision {
	priorHash, hadPrior := prior[item.to]
	info, err := os.Lstat(item.to)
	if errors.Is(err, os.ErrNotExist) {
		return decisionApply
	}
	if err != nil || !info.Mode().IsRegular() {
		return decisionCollision
	}
	targetHash, err := hashFile(item.to)
	if err != nil {
		return decisionCollision
	}
	if !hadPrior {
		return decisionCollision
	}
	if targetHash != priorHash {
		return decisionCollision
	}
	if priorHash == item.hash {
		return decisionSkip
	}
	return decisionApply
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".import-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := fsatomic.Replace(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func writeItem(item fileItem) error {
	data, err := os.ReadFile(item.from)
	if err != nil {
		return fmt.Errorf("read source %s: %w", item.from, err)
	}
	if err := atomicWrite(item.to, data); err != nil {
		return fmt.Errorf("write target %s: %w", item.to, err)
	}
	if err := os.Chtimes(item.to, item.modTime, item.modTime); err != nil {
		return fmt.Errorf("preserve mtime for %s: %w", item.to, err)
	}
	return nil
}

func buildPlan(sourceProject, sourceUser, targetProject, targetUser string) (copyPlan, error) {
	plan := copyPlan{indexTo: memory.IndexFile(targetProject)}
	if _, err := requireDir(sourceProject); err != nil {
		return plan, err
	}
	if _, err := requireDir(sourceUser); err != nil {
		return plan, err
	}

	rootEntries, err := os.ReadDir(sourceProject)
	if err != nil {
		return plan, fmt.Errorf("read source project: %w", err)
	}
	for _, entry := range rootEntries {
		name := entry.Name()
		switch {
		case name == "MEMORY.md":
			plan.indexFrom = filepath.Join(sourceProject, name)
			if data, err := os.ReadFile(plan.indexFrom); err == nil {
				plan.indexHash = hashBytes(data)
			}
		case name == "project.json":
			plan.omitted = append(plan.omitted, name+": project identity is target-owned; regenerated from the target root")
		case name == "archive":
			plan.omitted = append(plan.omitted, name+": housekeeping archive is not imported")
		case name == ".engine":
			plan.omitted = append(plan.omitted, name+": private engine state is not imported")
		case entry.IsDir():
			if name != "topics" && name != "sessions" && name != "pages" {
				plan.omitted = append(plan.omitted, name+": unsupported directory")
			}
		case entry.Type()&os.ModeSymlink != 0:
			plan.omitted = append(plan.omitted, name+": symlink source is not imported")
		case strings.HasSuffix(name, ".md"):
			if err := plan.add(sourceProject, targetProject, name, kindFact); err != nil {
				return plan, err
			}
		default:
			plan.omitted = append(plan.omitted, name+": unsupported top-level file")
		}
	}

	for _, spec := range []struct {
		dir  string
		kind string
	}{
		{"topics", kindFact},
		{"sessions", kindDigest},
		{"pages", kindPage},
	} {
		if err := plan.addDir(filepath.Join(sourceProject, spec.dir), filepath.Join(targetProject, spec.dir), spec.kind); err != nil {
			return plan, err
		}
	}

	for _, spec := range []struct {
		name string
		kind string
	}{
		{"chains.jsonl", kindChain},
		{"chains.prev.jsonl", kindChain},
		{"capture.json", kindCapture},
		{"capture_attempt.json", kindCapture},
		{"journal.jsonl", kindJournal},
		{"journal.prev.jsonl", kindJournal},
	} {
		plan.addFixed(sourceProject, targetProject, spec.name, spec.kind)
	}
	plan.addFixed(sourceUser, targetUser, "user.md", kindUser)
	return plan, nil
}

func (p *copyPlan) add(sourceDir, targetDir, name, kind string) error {
	from := filepath.Join(sourceDir, name)
	info, err := os.Lstat(from)
	if err != nil {
		return fmt.Errorf("stat source %s: %w", from, err)
	}
	if !info.Mode().IsRegular() {
		p.omitted = append(p.omitted, from+": source is not a regular file")
		return nil
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return fmt.Errorf("read source %s: %w", from, err)
	}
	p.files = append(p.files, fileItem{
		from: from, to: filepath.Join(targetDir, name), kind: kind,
		hash: hashBytes(data), modTime: info.ModTime(),
	})
	return nil
}

func (p *copyPlan) addFixed(sourceDir, targetDir, name, kind string) {
	from := filepath.Join(sourceDir, name)
	info, err := os.Lstat(from)
	if err != nil || !info.Mode().IsRegular() {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			p.omitted = append(p.omitted, from+": unreadable source")
		}
		return
	}
	data, err := os.ReadFile(from)
	if err != nil {
		p.omitted = append(p.omitted, from+": unreadable source")
		return
	}
	p.files = append(p.files, fileItem{
		from: from, to: filepath.Join(targetDir, name), kind: kind,
		hash: hashBytes(data), modTime: info.ModTime(),
	})
}

func (p *copyPlan) addDir(sourceDir, targetDir, kind string) error {
	entries, err := os.ReadDir(sourceDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read source dir %s: %w", sourceDir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if kind != kindFact || !memory.ValidTopicName(name) {
				p.omitted = append(p.omitted, filepath.Join(sourceDir, name)+": unsupported directory")
				continue
			}
			inner := filepath.Join(sourceDir, name)
			files, err := os.ReadDir(inner)
			if err != nil {
				return fmt.Errorf("read topic %s: %w", inner, err)
			}
			for _, file := range files {
				if file.IsDir() || !strings.HasSuffix(file.Name(), ".md") {
					p.omitted = append(p.omitted, filepath.Join(inner, file.Name())+": unsupported file")
					continue
				}
				if err := p.add(inner, filepath.Join(targetDir, name), file.Name(), kindFact); err != nil {
					return err
				}
			}
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			p.omitted = append(p.omitted, filepath.Join(sourceDir, name)+": symlink source is not imported")
			continue
		}
		if !strings.HasSuffix(name, ".md") {
			p.omitted = append(p.omitted, filepath.Join(sourceDir, name)+": unsupported file")
			continue
		}
		if err := p.add(sourceDir, targetDir, name, kind); err != nil {
			return err
		}
	}
	return nil
}

func absoluteDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("directory is required")
	}
	return filepath.Abs(path)
}

func requireDir(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", path)
	}
	return info, nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func manifestPath(targetProject, sourceProject, sourceUser string) string {
	sum := sha256.Sum256([]byte(sourceProject + "\n" + sourceUser))
	return filepath.Join(targetProject, ".engine", "imports", hex.EncodeToString(sum[:])+".json")
}

func readApplied(path string) (map[string]string, error) {
	applied := map[string]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return applied, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	for _, mapping := range report.Mappings {
		if mapping.Kind != kindIndex {
			applied[mapping.To] = mapping.Hash
		}
	}
	return applied, nil
}

func writeManifest(path string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := atomicWrite(path, append(data, '\n')); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// Status reads the most recent import manifest under targetDir.
func Status(targetDir string) (Report, bool, error) {
	absolute, err := absoluteDir(targetDir)
	if err != nil {
		return Report{}, false, err
	}
	dir := filepath.Join(absolute, ".engine", "imports")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return Report{}, false, nil
	}
	if err != nil {
		return Report{}, false, fmt.Errorf("read import manifests: %w", err)
	}
	var newest os.FileInfo
	var newestPath string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return Report{}, false, fmt.Errorf("stat import manifest: %w", err)
		}
		if newest == nil || info.ModTime().After(newest.ModTime()) ||
			(info.ModTime().Equal(newest.ModTime()) && entry.Name() > filepath.Base(newestPath)) {
			newest = info
			newestPath = filepath.Join(dir, entry.Name())
		}
	}
	if newestPath == "" {
		return Report{}, false, nil
	}
	data, err := os.ReadFile(newestPath)
	if err != nil {
		return Report{}, false, fmt.Errorf("read import manifest: %w", err)
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return Report{}, false, fmt.Errorf("parse import manifest: %w", err)
	}
	return report, true, nil
}
