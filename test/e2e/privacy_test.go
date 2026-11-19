package e2e

// audit: behavior - engine filtering keeps private, synthetic, and secret text out of durable jobs and model context.
// audit: regression - a synthetic private span reached a durable job input and a leased prompt.
// audit: coverage gap - existing transport tests ingest clean text and never seed version-0 jobs after an upgrade.
// audit: seam - the tests drive the built binary with JSON-RPC frames and inspect the durable files it writes.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ingestCounts(t *testing.T, resp response) (accepted, dropped int) {
	t.Helper()
	var out struct {
		Accepted int `json:"accepted"`
		Dropped  int `json:"dropped"`
	}
	if err := json.Unmarshal(ok(t, resp), &out); err != nil {
		t.Fatal(err)
	}
	return out.Accepted, out.Dropped
}

func jobPath(d dirs, id string) string {
	return filepath.Join(d.project, ".engine", "jobs", id+".json")
}

func inputPath(d dirs, id string) string {
	return filepath.Join(d.project, ".engine", "jobs", id+".input.json")
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateTextFilteredFromJobsAndRecall(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1})

	credential := "AKIA1234567890ABCDEF"
	accepted, dropped := ingestCounts(t, c.call(t, "events.ingest", map[string]any{
		"records": []record{
			{ID: "r1", SessionID: "s1", Generation: "g1", Time: "2026-11-18T00:00:00Z", Role: "user",
				Text: "public note <private>secret-alpha</private> tail " + credential},
			{ID: "r2", SessionID: "s1", Generation: "g1", Time: "2026-11-18T00:00:01Z", Role: "user",
				Text: "<private>fully hidden beta</private>"},
			{ID: "r3", SessionID: "s1", Generation: "g1", Time: "2026-11-18T00:00:02Z", Role: "user",
				Text: "injected memory block", Synthetic: true},
		},
		"boundaries": []boundary{{SessionID: "s1", Generation: "g1", Kind: "complete",
			Time: "2026-11-18T00:00:03Z", Available: true}},
	}))
	if accepted != 1 || dropped != 2 {
		t.Fatalf("ingest counts accepted=%d dropped=%d, want 1 and 2", accepted, dropped)
	}

	raw := ok(t, c.call(t, "jobs.next", map[string]any{"generation": true}))
	var leased struct {
		Job   *jobView `json:"job"`
		Lease string   `json:"lease"`
	}
	if err := json.Unmarshal(raw, &leased); err != nil {
		t.Fatal(err)
	}
	if leased.Job == nil || leased.Job.Kind != "fact" {
		t.Fatalf("no fact job leased: %s", raw)
	}
	for _, want := range []string{"public note", "tail", "[REDACTED]"} {
		if !strings.Contains(leased.Job.Prompt, want) {
			t.Fatalf("leased prompt lacks %q: %s", want, leased.Job.Prompt)
		}
	}
	hidden := []string{"secret-alpha", "fully hidden beta", "injected memory block", credential}
	for _, text := range hidden {
		if strings.Contains(leased.Job.Prompt, text) {
			t.Fatalf("leased prompt exposes %q: %s", text, leased.Job.Prompt)
		}
	}
	stored, _ := readJSONFile(t, inputPath(d, leased.Job.ID))["prompt"].(string)
	for _, text := range hidden {
		if strings.Contains(stored, text) {
			t.Fatalf("durable job input exposes %q: %s", text, stored)
		}
	}

	added := appendFact(t, c, "p1", "visible <private>hidden-gamma</private> note", "visible body text")
	for _, method := range []string{"read", "search"} {
		params := execParams{Target: "project", Operation: method, Name: added.Name, Query: "visible"}
		result := string(ok(t, c.call(t, "memory.execute", params)))
		if strings.Contains(result, "hidden-gamma") {
			t.Fatalf("%s exposes private text: %s", method, result)
		}
	}
	section := string(ok(t, c.call(t, "context.get", map[string]any{"refresh": true})))
	if strings.Contains(section, "hidden-gamma") {
		t.Fatalf("context exposes private text: %s", section)
	}
}

func TestPrivateEditEvidenceRecordsGap(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1})

	evidence, err := json.Marshal(map[string]string{
		"path": "notes.txt", "oldString": "before",
		"newString": "after <private>secret-delta</private>",
	})
	if err != nil {
		t.Fatal(err)
	}
	call, err := json.Marshal(map[string]any{
		"id": "edit-1", "name": "edit", "class": "edit",
		"outcome": "success", "editEvidence": evidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	ok(t, c.call(t, "events.ingest", map[string]any{
		"records": []record{{ID: "e1", SessionID: "s1", Generation: "g1",
			Time: "2026-11-18T00:00:00Z", Role: "user", Text: "an edit happened",
			ToolCalls: []json.RawMessage{call}}},
		"boundaries": []boundary{{SessionID: "s1", Generation: "g1", Kind: "complete",
			Time: "2026-11-18T00:00:01Z", Available: true}},
	}))

	raw := ok(t, c.call(t, "jobs.next", map[string]any{"generation": false}))
	var leased struct {
		Job   *jobView `json:"job"`
		Lease string   `json:"lease"`
	}
	if err := json.Unmarshal(raw, &leased); err != nil {
		t.Fatal(err)
	}
	if leased.Job == nil || leased.Job.Kind != "revision" {
		t.Fatalf("no revision job leased: %s", raw)
	}
	ok(t, c.call(t, "jobs.complete", map[string]any{
		"jobID": leased.Job.ID, "lease": leased.Lease, "output": "",
	}))

	statusRaw := ok(t, c.call(t, "status.get", map[string]any{}))
	var status struct {
		Conflicts int `json:"conflicts"`
	}
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		t.Fatal(err)
	}
	if status.Conflicts == 0 {
		t.Fatalf("no edit-gap conflict recorded: %s", statusRaw)
	}
	entries, err := os.ReadDir(filepath.Join(d.project, ".engine", "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	gapFound := false
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") || strings.HasSuffix(entry.Name(), ".input.json") {
			continue
		}
		job := readJSONFile(t, filepath.Join(d.project, ".engine", "jobs", entry.Name()))
		if job["Kind"] == "gap" && strings.Contains(job["Error"].(string), "unsupported edit evidence") {
			gapFound = true
		}
	}
	if !gapFound {
		t.Fatal("edit-gap conflict job not found")
	}
	err = filepath.WalkDir(d.project, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "secret-delta") {
			t.Errorf("durable file exposes private edit evidence: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVersionZeroJobsBlockedAfterUpgrade(t *testing.T) {
	d := tempDirs(t)
	old := time.Now().Add(-time.Hour)
	legacy := []struct {
		id, state, lease  string
		retryAt, leasedAt time.Time
	}{
		{id: "legacy-pending", state: "pending"},
		{id: "legacy-retry", state: "retry_wait", retryAt: old},
		{id: "legacy-leased", state: "leased", lease: "legacy-lease", leasedAt: old},
	}
	for _, job := range legacy {
		record := map[string]any{
			"id": job.id, "kind": "fact", "harnessID": "bound-harness",
			"sessionID": "s1", "generation": "g1", "phase": "fact",
			"state": job.state, "lease": job.lease, "inputHash": "stale-hash",
		}
		if !job.retryAt.IsZero() {
			record["retryAt"] = job.retryAt
		}
		writeJSONFile(t, jobPath(d, job.id), record)
		input := map[string]any{"seq": 1, "prompt": "legacy prompt"}
		if !job.leasedAt.IsZero() {
			input["leasedAt"] = job.leasedAt
		}
		writeJSONFile(t, inputPath(d, job.id), input)
	}

	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1})

	nextRaw := ok(t, c.call(t, "jobs.next", map[string]any{"generation": true}))
	var next struct {
		Job *jobView `json:"job"`
	}
	if err := json.Unmarshal(nextRaw, &next); err != nil {
		t.Fatal(err)
	}
	if next.Job != nil {
		t.Fatalf("version-0 job leased after upgrade: %s", nextRaw)
	}

	statusRaw := ok(t, c.call(t, "status.get", map[string]any{}))
	var status struct {
		Pending   int `json:"pending"`
		Conflicts int `json:"conflicts"`
	}
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		t.Fatal(err)
	}
	if status.Pending != 0 || status.Conflicts != len(legacy) {
		t.Fatalf("status after upgrade = %s, want %d conflicts", statusRaw, len(legacy))
	}

	failed := c.call(t, "jobs.complete", map[string]any{
		"jobID": "legacy-leased", "lease": "legacy-lease", "output": "applied anyway",
	})
	if failed.Error == nil {
		t.Fatal("blocked legacy job accepted a completion")
	}
	for _, job := range legacy {
		stored := readJSONFile(t, jobPath(d, job.id))
		if stored["State"] != "conflict" || stored["Error"] != "privacy_policy_required" || stored["Lease"] != "" {
			t.Fatalf("legacy job %s not blocked: %v", job.id, stored)
		}
	}
}
