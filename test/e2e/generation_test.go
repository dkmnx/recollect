package e2e

// audit: behavior - initialize and status report generation state and filter the generation capability.
// audit: regression - Setup advertised generation before it checked the host, so disabled hosts looked capable.
// audit: coverage - Existing transport tests never send generationState or inspect initialize capabilities.
// audit: seam - The tests call initialize, status.get, events.ingest, and jobs.next through the binary.

import (
	"encoding/json"
	"strings"
	"testing"
)

type generationParams struct {
	ProjectDir      string         `json:"projectDir"`
	UserDir         string         `json:"userDir"`
	HarnessID       string         `json:"harnessID"`
	Capabilities    []string       `json:"capabilities"`
	GenerationState string         `json:"generationState,omitempty"`
	Protocol        map[string]int `json:"protocol"`
}

func TestGenerationStateReportedBeforeJobs(t *testing.T) {
	for _, state := range []string{"disabled", "unavailable", "unknown"} {
		t.Run(state, func(t *testing.T) {
			d := tempDirs(t)
			c := start(t)
			defer c.cmd.Process.Kill()

			params := generationParams{
				ProjectDir: d.project, UserDir: d.user, HarnessID: "bound-harness",
				Capabilities: []string{"generation", "memory"},
				Protocol:     map[string]int{"major": 1, "minor": 1},
			}
			if state != "unknown" {
				params.GenerationState = state
			}
			raw := ok(t, c.call(t, "initialize", params))
			var result struct {
				GenerationState string   `json:"generationState"`
				Capabilities    []string `json:"capabilities"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if result.GenerationState != state {
				t.Fatalf("initialize generationState = %q, want %q: %s", result.GenerationState, state, raw)
			}
			for _, capability := range result.Capabilities {
				if capability == "generation" {
					t.Fatalf("generation advertised for %s: %s", state, raw)
				}
			}

			statusRaw := ok(t, c.call(t, "status.get", map[string]any{}))
			var status struct {
				GenerationState string `json:"generationState"`
			}
			if err := json.Unmarshal(statusRaw, &status); err != nil {
				t.Fatal(err)
			}
			if status.GenerationState != state {
				t.Fatalf("status generationState = %q, want %q: %s", status.GenerationState, state, statusRaw)
			}

			ok(t, c.call(t, "events.ingest", map[string]any{
				"records": []record{{ID: "r1", SessionID: "s1", Generation: "g1",
					Time: "2026-11-18T00:00:00Z", Role: "user", Text: "hello"}},
				"boundaries": []boundary{{SessionID: "s1", Generation: "g1", Kind: "complete",
					Time: "2026-11-18T00:00:01Z", Available: true}},
			}))
			nextRaw := ok(t, c.call(t, "jobs.next", map[string]any{"generation": false}))
			var next struct {
				Job *jobView `json:"job"`
			}
			if err := json.Unmarshal(nextRaw, &next); err != nil {
				t.Fatal(err)
			}
			if next.Job == nil || next.Job.Kind != "revision" {
				t.Fatalf("model job leased without generation for %s: %s", state, nextRaw)
			}
		})
	}
}

func TestEnabledGenerationKeepsCapability(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	raw := ok(t, c.call(t, "initialize", generationParams{
		ProjectDir: d.project, UserDir: d.user, HarnessID: "bound-harness",
		Capabilities: []string{"generation", "memory"}, GenerationState: "enabled",
		Protocol: map[string]int{"major": 1, "minor": 1},
	}))
	var result struct {
		GenerationState string   `json:"generationState"`
		Capabilities    []string `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.GenerationState != "enabled" || !strings.Contains(strings.Join(result.Capabilities, ","), "generation") {
		t.Fatalf("enabled generation not reported: %s", raw)
	}
}

func TestJobsCompleteRejectsRawFailure(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "bound-harness", map[string]int{"major": 1})
	ok(t, c.call(t, "events.ingest", map[string]any{
		"records": []record{{ID: "r1", SessionID: "s1", Generation: "g1",
			Time: "2026-11-18T00:00:00Z", Role: "user", Text: "hello"}},
		"boundaries": []boundary{{SessionID: "s1", Generation: "g1", Kind: "complete",
			Time: "2026-11-18T00:00:01Z", Available: true}},
	}))
	raw := ok(t, c.call(t, "jobs.next", map[string]any{"generation": true}))
	var leased struct {
		Job   *jobView `json:"job"`
		Lease string   `json:"lease"`
	}
	if err := json.Unmarshal(raw, &leased); err != nil || leased.Job == nil {
		t.Fatalf("lease: %v %s", err, raw)
	}
	wantCode(t, c.call(t, "jobs.complete", map[string]any{
		"jobID": leased.Job.ID, "lease": leased.Lease, "failure": "provider said: secret header",
	}), -32602)
}
