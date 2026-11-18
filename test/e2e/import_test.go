package e2e

// audit: behavior - maintenance.execute import and import-status actions over the built binary.
// audit: regression - existing maintenance status action and other RPC methods keep their contracts.
// audit: coverage gap - dry run, apply, status, and invalid params across the process boundary.
// audit: seam - tests drive the real built binary with newline-delimited JSON-RPC frames.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dkmnx/recollect/internal/imports"
	"github.com/dkmnx/recollect/internal/memory"
)

type maintenanceImportParams struct {
	Action           string `json:"action"`
	SourceProjectDir string `json:"sourceProjectDir"`
	SourceUserDir    string `json:"sourceUserDir"`
	DryRun           bool   `json:"dryRun"`
}

type importStatusResult struct {
	Report imports.Report `json:"report"`
	Exists bool           `json:"exists"`
}

func buildImportSource(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	project := filepath.Join(base, "source-project")
	user := filepath.Join(base, "source-user")
	store := memory.NewStore(memory.Options{
		ProjectDir: project,
		UserDir:    user,
		Meta:       memory.ProjectMetaFor(project),
	})
	if _, err := store.AppendFact(memory.Fact{
		Name: "rpc-fact", Description: "RPC fact", Body: "Imported through the binary.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendUser("rpc import", "User text through the binary."); err != nil {
		t.Fatal(err)
	}
	return project, user
}

func decodeImportReport(t *testing.T, raw json.RawMessage) imports.Report {
	t.Helper()
	var report imports.Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestMaintenanceImportThroughBinary(t *testing.T) {
	srcProject, srcUser := buildImportSource(t)
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "harness-import", map[string]int{"major": 1})

	dry := decodeImportReport(t, ok(t, c.call(t, "maintenance.execute", maintenanceImportParams{
		Action: "import", SourceProjectDir: srcProject, SourceUserDir: srcUser, DryRun: true,
	})))
	if !dry.DryRun || len(dry.Mappings) == 0 {
		t.Fatalf("dry import result: %+v", dry)
	}
	if _, err := os.Stat(d.project); !os.IsNotExist(err) {
		t.Fatalf("dry import created target dir: %v", err)
	}

	applied := decodeImportReport(t, ok(t, c.call(t, "maintenance.execute", maintenanceImportParams{
		Action: "import", SourceProjectDir: srcProject, SourceUserDir: srcUser,
	})))
	if len(applied.Applied) == 0 {
		t.Fatal("import applied nothing through the binary")
	}

	check := memory.NewStore(memory.Options{ProjectDir: d.project, UserDir: d.user})
	facts, err := check.ListFacts()
	if err != nil || len(facts) != 1 || facts[0].Name != "rpc-fact" {
		t.Fatalf("target fact not readable: %+v %v", facts, err)
	}

	statusRaw := ok(t, c.call(t, "maintenance.execute", maintenanceImportParams{Action: "import-status"}))
	var status importStatusResult
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		t.Fatal(err)
	}
	if !status.Exists || status.Report.SourceProjectDir != srcProject || status.Report.SourceUserDir != srcUser {
		t.Fatalf("import-status result: %+v", status)
	}
}

func TestMaintenanceImportRejectsMissingSource(t *testing.T) {
	d := tempDirs(t)
	c := start(t)
	defer c.cmd.Process.Kill()
	initialize(t, c, d, "harness-import", map[string]int{"major": 1})
	wantCode(t, c.call(t, "maintenance.execute", maintenanceImportParams{Action: "import"}), -32602)
}
