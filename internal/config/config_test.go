package config_test

// audit: behavior - config loads defaults, a JSON file, env overrides, and a secret-free summary.
// audit: regression - an unknown key or invalid value could silently change the search backend or leak an API key.
// audit: coverage gap - the binary read RECOLLECT_SEARCH_INDEX directly and had no config file.
// audit: seam - tests call Load and LoadUserDir with temp files and t.Setenv.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkmnx/recollect/internal/config"
)

func writeConfig(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("RECOLLECT_SEARCH_INDEX", "")
	t.Setenv("RECOLLECT_GENERATION", "")
}

func TestDefault(t *testing.T) {
	cfg := config.Default()
	if cfg.Search.Backend != "lexical" || cfg.Generation.Mode != "off" || cfg.Generation.TimeoutSeconds != 120 {
		t.Fatalf("default = %+v", cfg)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := config.Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Search.Backend != "lexical" || cfg.Generation.Mode != "off" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadParsesFile(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `{
		"search": {"backend": "sqlite"},
		"embedding": {"baseURL": "http://127.0.0.1:11434/v1", "model": "nomic", "dimensions": 768, "apiKeyEnv": "EMBED_KEY"},
		"generation": {"mode": "command", "command": ["claude", "-p"], "timeoutSeconds": 30}
	}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Search.Backend != "sqlite" {
		t.Fatalf("backend = %q", cfg.Search.Backend)
	}
	if cfg.Embedding.Dimensions != 768 || cfg.Embedding.APIKeyEnv != "EMBED_KEY" {
		t.Fatalf("embedding = %+v", cfg.Embedding)
	}
	if cfg.Generation.Mode != "command" || len(cfg.Generation.Command) != 2 || cfg.Generation.TimeoutSeconds != 30 {
		t.Fatalf("generation = %+v", cfg.Generation)
	}
}

func TestLoadEmptyFileUsesDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := config.Load(writeConfig(t, "\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Search.Backend != "lexical" {
		t.Fatalf("backend = %q", cfg.Search.Backend)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	clearEnv(t)
	_, err := config.Load(writeConfig(t, `{"search": {"backend": "sqlite"}, "bogus": true}`))
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("err = %v, want the unknown key named", err)
	}
}

func TestLoadRejectsInlineAPIKey(t *testing.T) {
	clearEnv(t)
	_, err := config.Load(writeConfig(t, `{"embedding": {"apiKey": "secret"}}`))
	if err == nil || !strings.Contains(err.Error(), "apiKeyEnv") {
		t.Fatalf("err = %v, want an apiKeyEnv hint", err)
	}
}

func TestLoadRejectsAPIKeyEnvValue(t *testing.T) {
	clearEnv(t)
	_, err := config.Load(writeConfig(t, `{"embedding": {"apiKeyEnv": "sk-secret"}}`))
	if err == nil || !strings.Contains(err.Error(), "apiKeyEnv") {
		t.Fatalf("err = %v, want an apiKeyEnv name error", err)
	}
}

func TestLoadRejectsURLUserInfo(t *testing.T) {
	clearEnv(t)
	_, err := config.Load(writeConfig(t, `{"embedding": {"baseURL": "https://user:pass@example/v1"}}`))
	if err == nil || !strings.Contains(err.Error(), "apiKeyEnv") {
		t.Fatalf("err = %v, want a credentials error", err)
	}
}

func TestLoadRejectsInvalidBackend(t *testing.T) {
	clearEnv(t)
	if _, err := config.Load(writeConfig(t, `{"search": {"backend": "nope"}}`)); err == nil {
		t.Fatal("invalid backend accepted")
	}
}

func TestLoadRejectsCommandModeWithoutCommand(t *testing.T) {
	clearEnv(t)
	if _, err := config.Load(writeConfig(t, `{"generation":{"mode":"command"}}`)); err == nil {
		t.Fatal("command mode without a command accepted")
	}
	if _, err := config.Load(writeConfig(t, `{"generation":{"mode":"command","command":["  "]}}`)); err == nil {
		t.Fatal("blank executable accepted")
	}
}

func TestLoadRejectsRelativeBaseURL(t *testing.T) {
	clearEnv(t)
	if _, err := config.Load(writeConfig(t, `{"embedding":{"baseURL":"localhost:11434"}}`)); err == nil {
		t.Fatal("relative base URL accepted")
	}
}

func TestLoadRejectsHTTPModeWithoutEndpoint(t *testing.T) {
	clearEnv(t)
	if _, err := config.Load(writeConfig(t, `{"generation":{"mode":"http"}}`)); err == nil {
		t.Fatal("http mode without an endpoint accepted")
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("RECOLLECT_SEARCH_INDEX", "hybrid")
	t.Setenv("RECOLLECT_GENERATION", "off")
	path := writeConfig(t, `{
		"search": {"backend": "sqlite"},
		"generation": {"mode": "command", "command": ["claude"], "timeoutSeconds": 30}
	}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Search.Backend != "hybrid" {
		t.Fatalf("backend = %q, want hybrid", cfg.Search.Backend)
	}
	if cfg.Generation.Mode != "off" {
		t.Fatalf("mode = %q, want off", cfg.Generation.Mode)
	}
}

func TestEnvOverrideErrorNamesVariable(t *testing.T) {
	t.Setenv("RECOLLECT_SEARCH_INDEX", "bogus")
	_, err := config.Load(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil || !strings.Contains(err.Error(), "RECOLLECT_SEARCH_INDEX") {
		t.Fatalf("err = %v, want the variable named", err)
	}
}

func TestFileErrorIsNotMaskedByEnv(t *testing.T) {
	t.Setenv("RECOLLECT_GENERATION", "off")
	_, err := config.Load(writeConfig(t, `{"generation": {"mode": "invalid"}}`))
	if err == nil || !strings.Contains(err.Error(), "generation.mode") {
		t.Fatalf("err = %v, want the file error", err)
	}
}

func TestSummaryShowsSecretStateWithoutValue(t *testing.T) {
	t.Setenv("EMBED_KEY", "super-secret")
	cfg := config.Config{
		Search: config.Search{Backend: "hybrid"},
		Embedding: config.Embedding{
			BaseURL: "http://example", Model: "m", Dimensions: 3, APIKeyEnv: "EMBED_KEY",
		},
		Generation: config.Generation{
			Mode: "http", HTTP: config.HTTP{BaseURL: "http://example", Model: "g", APIKeyEnv: "MISSING_KEY"},
			TimeoutSeconds: 10,
		},
	}
	lines := strings.Join(cfg.Summary(os.Getenv), "\n")
	if !strings.Contains(lines, "embedding.apiKeyEnv: set") || !strings.Contains(lines, "generation.http.apiKeyEnv: unset") {
		t.Fatalf("summary = %q", lines)
	}
	if strings.Contains(lines, "super-secret") || strings.Contains(lines, "EMBED_KEY") {
		t.Fatal("summary leaked a key name or value")
	}
}

func TestSummaryNeverPrintsAPIKeyEnvName(t *testing.T) {
	cfg := config.Default()
	cfg.Embedding.APIKeyEnv = "skprojsecret123"
	lines := strings.Join(cfg.Summary(os.Getenv), "\n")
	if strings.Contains(lines, "skprojsecret123") {
		t.Fatalf("summary printed the apiKeyEnv value: %q", lines)
	}
	if !strings.Contains(lines, "embedding.apiKeyEnv: unset") {
		t.Fatalf("summary = %q", lines)
	}
}

func TestSummaryRedactsEndpointsAndCommand(t *testing.T) {
	cfg := config.Config{
		Search: config.Search{Backend: "hybrid"},
		Embedding: config.Embedding{
			BaseURL: "http://example/v1?api_key=SENTINEL", Model: "m", Dimensions: 3, APIKeyEnv: "EMBED_KEY",
		},
		Generation: config.Generation{
			Mode: "command", Command: []string{"claude", "--token", "SENTINEL"}, TimeoutSeconds: 10,
		},
	}
	lines := strings.Join(cfg.Summary(os.Getenv), "\n")
	if strings.Contains(lines, "SENTINEL") {
		t.Fatalf("summary leaked a credential: %q", lines)
	}
	if !strings.Contains(lines, "embedding.baseURL: http://example") {
		t.Fatalf("summary = %q", lines)
	}
	if !strings.Contains(lines, "generation.command: configured (3 arguments)") {
		t.Fatalf("summary = %q", lines)
	}
}

func TestSummaryMarksInvalidAPIKeyEnv(t *testing.T) {
	cfg := config.Default()
	cfg.Embedding.APIKeyEnv = "sk-secret"
	lines := strings.Join(cfg.Summary(os.Getenv), "\n")
	if !strings.Contains(lines, "embedding.apiKeyEnv: invalid") {
		t.Fatalf("summary = %q", lines)
	}
	if strings.Contains(lines, "sk-secret") {
		t.Fatal("summary printed an invalid apiKeyEnv value")
	}
}

func TestLoadUserDir(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"search":{"backend":"sqlite"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := config.LoadUserDir(dir)
	if err != nil {
		t.Fatalf("LoadUserDir: %v", err)
	}
	if cfg.Search.Backend != "sqlite" {
		t.Fatalf("backend = %q", cfg.Search.Backend)
	}
}
