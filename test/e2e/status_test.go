package e2e

// audit: behavior - status.get reports store counts, job states, generation state, and classified failures without writes.
// audit: regression - status used Ping, which created a probe file, and leaked a formatted lastError string.
// audit: coverage - Existing tests only read pending and conflicts and never inspect the durable tree around status.
// audit: seam - Tests drive status.get through the binary and inspect the file tree it must leave unchanged.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type statusView struct {
	Health           string         `json:"health"`
	Pending          int            `json:"pending"`
	Conflicts        int            `json:"conflicts"`
	GenerationState  string         `json:"generationState"`
	JobStates        map[string]int `json:"jobStates"`
	PrivacyBlocked   int            `json:"privacyBlocked"`
	Facts            int            `json:"facts"`
	UserBytes        int            `json:"userBytes"`
	Digests          int            `json:"digests"`
	PagesNeedRefresh []string       `json:"pagesNeedRefresh"`
	Limits           map[string]int `json:"limits"`
	LastErrorCode    string         `json:"lastErrorCode"`
}

func readStatus(t *testing.T, c *client) statusView {
	t.Helper()
	raw := ok(t, c.call(t, "status.get", map[string]any{}))
	var out statusView
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func dirMtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

func TestStatusIsReadOnlyAndBounded(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1, "minor": 1})

	before := readStatus(t, c)
	if before.Health != "ok" || before.Facts != 0 || before.UserBytes != 0 || before.Digests != 0 {
		t.Fatalf("fresh status = %+v", before)
	}
	if before.LastErrorCode != "" {
		t.Fatalf("fresh status has an error code: %+v", before)
	}
	for _, state := range []string{"pending", "leased", "applied", "retry_wait", "conflict", "cancelled"} {
		if count, present := before.JobStates[state]; !present || count != 0 {
			t.Fatalf("job state %s missing or nonzero: %+v", state, before.JobStates)
		}
	}
	if len(before.PagesNeedRefresh) == 0 {
		t.Fatal("catalog pages needing refresh not reported")
	}
	if before.Limits["maxSearchResults"] <= 0 || before.Limits["digestTTLDays"] <= 0 {
		t.Fatalf("effective limits not reported: %+v", before.Limits)
	}

	mtime := dirMtime(t, d.project)
	_ = readStatus(t, c)
	if !dirMtime(t, d.project).Equal(mtime) {
		t.Fatal("status.get changed the project directory")
	}

	appendFact(t, c, "s1", "status fact", "body")
	after := readStatus(t, c)
	if after.Facts != 1 {
		t.Fatalf("fact count after append = %d", after.Facts)
	}

	wantCode(t, c.call(t, "memory.execute", execParams{
		Target: "project", Operation: "read", Name: "missing-fact",
	}), -32602)
	failed := readStatus(t, c)
	if failed.LastErrorCode != "fact_not_found" {
		t.Fatalf("lastErrorCode = %q, want fact_not_found", failed.LastErrorCode)
	}
}

func TestStatusReportsPrivacyConflicts(t *testing.T) {
	d := tempDirs(t)
	writeJSONFile(t, jobPath(d, "legacy-status"), map[string]any{
		"ID": "legacy-status", "Kind": "fact", "HarnessID": "bound-harness",
		"State": "pending", "InputHash": "stale",
	})
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1, "minor": 1})
	_ = c.call(t, "jobs.next", map[string]any{"generation": false})

	status := readStatus(t, c)
	if status.PrivacyBlocked != 1 || status.Conflicts != 1 {
		t.Fatalf("privacy conflicts = %+v", status)
	}
	if status.JobStates["conflict"] != 1 {
		t.Fatalf("conflict state count = %+v", status.JobStates)
	}
}

// audit: behavior - status reports storage_failure for an unreadable store and writes no probe file.
// audit: regression - the unreadable store was built with chmod 0o000, which Windows ignores, so the daemon looked healthy.
// audit: coverage gap - no test constructed a broken store through a path both NTFS and POSIX reject.
// audit: seam - status.get over the binary while a plain file occupies the topics directory path.
func TestStatusReportsStorageFailureWithoutWriting(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1, "minor": 1})
	if err := os.MkdirAll(d.project, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file at the topics path breaks fact enumeration on every platform;
	// chmod 0o000 leaves the store readable on Windows.
	blocker := filepath.Join(d.project, "topics")
	if err := os.RemoveAll(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocker, []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}

	resp := c.call(t, "status.get", map[string]any{})
	if resp.Error == nil {
		t.Fatalf("unreadable store returned a status: %s", resp.Result)
	}
	if resp.Error.Code != -32006 || resp.Error.Message != "storage_failure" {
		t.Fatalf("unreadable store error = %d %s", resp.Error.Code, resp.Error.Message)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(d.project, ".ping")); !os.IsNotExist(err) {
		t.Fatalf("status created a probe file: %v", err)
	}
}
