// Package config loads the optional user config file for cmd/recollect.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Config is the effective process configuration.
type Config struct {
	Search     Search     `json:"search"`
	Embedding  Embedding  `json:"embedding"`
	Generation Generation `json:"generation"`
}

// Search selects the derived search index.
type Search struct {
	Backend string `json:"backend"`
}

// Embedding identifies an OpenAI-compatible embeddings endpoint.
type Embedding struct {
	BaseURL    string `json:"baseURL"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	APIKeyEnv  string `json:"apiKeyEnv"`
}

// Generation configures the job generator.
type Generation struct {
	Mode           string   `json:"mode"`
	Command        []string `json:"command"`
	HTTP           HTTP     `json:"http"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

// HTTP identifies an OpenAI-compatible generation endpoint.
type HTTP struct {
	BaseURL   string `json:"baseURL"`
	Model     string `json:"model"`
	APIKeyEnv string `json:"apiKeyEnv"`
}

// Default returns the configuration used when no file is present.
func Default() Config {
	return Config{
		Search:     Search{Backend: "lexical"},
		Generation: Generation{Mode: "off", TimeoutSeconds: 120},
	}
}

// Load reads path. A missing or empty file returns the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return Config{}, err
	case len(strings.TrimSpace(string(raw))) == 0:
	default:
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			if strings.Contains(err.Error(), `unknown field "apiKey"`) {
				return Config{}, fmt.Errorf("parse %s: %w (use apiKeyEnv to name an environment variable)", path, err)
			}
			return Config{}, fmt.Errorf("parse %s: %w", path, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return Config{}, fmt.Errorf("parse %s: unexpected trailing data", path)
		}
		if err := validate(cfg); err != nil {
			return Config{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	cfg, err = applyEnv(cfg, os.Getenv)
	if err != nil {
		return Config{}, err
	}
	if err := validate(cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := validateEffective(cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// LoadUserDir reads <userDir>/config.json.
func LoadUserDir(userDir string) (Config, error) {
	return Load(filepath.Join(userDir, "config.json"))
}

func applyEnv(cfg Config, getenv func(string) string) (Config, error) {
	if backend := strings.TrimSpace(getenv("RECOLLECT_SEARCH_INDEX")); backend != "" {
		switch backend {
		case "lexical", "sqlite", "hybrid":
			cfg.Search.Backend = backend
		default:
			return Config{}, fmt.Errorf("RECOLLECT_SEARCH_INDEX must be lexical, sqlite, or hybrid, got %q", backend)
		}
	}
	if strings.TrimSpace(getenv("RECOLLECT_GENERATION")) == "off" {
		cfg.Generation.Mode = "off"
	}
	return cfg, nil
}

func validate(cfg Config) error {
	switch cfg.Search.Backend {
	case "lexical", "sqlite", "hybrid":
	default:
		return fmt.Errorf("search.backend must be lexical, sqlite, or hybrid, got %q", cfg.Search.Backend)
	}
	switch cfg.Generation.Mode {
	case "off":
	case "command":
		if len(cfg.Generation.Command) == 0 || strings.TrimSpace(cfg.Generation.Command[0]) == "" {
			return errors.New("generation.command[0] must name an executable when generation.mode is command")
		}
	case "http":
		if strings.TrimSpace(cfg.Generation.HTTP.BaseURL) == "" || strings.TrimSpace(cfg.Generation.HTTP.Model) == "" {
			return errors.New("generation.http.baseURL and generation.http.model are required when generation.mode is http")
		}
	default:
		return fmt.Errorf("generation.mode must be off, command, or http, got %q", cfg.Generation.Mode)
	}
	if cfg.Embedding.Dimensions < 0 {
		return errors.New("embedding.dimensions must be zero or greater")
	}
	if cfg.Generation.TimeoutSeconds <= 0 {
		return errors.New("generation.timeoutSeconds must be greater than zero")
	}
	if err := validateAPIKeyEnv("embedding.apiKeyEnv", cfg.Embedding.APIKeyEnv); err != nil {
		return err
	}
	if err := validateAPIKeyEnv("generation.http.apiKeyEnv", cfg.Generation.HTTP.APIKeyEnv); err != nil {
		return err
	}
	if err := validateBaseURL("embedding.baseURL", cfg.Embedding.BaseURL); err != nil {
		return err
	}
	if err := validateBaseURL("generation.http.baseURL", cfg.Generation.HTTP.BaseURL); err != nil {
		return err
	}
	return nil
}

// validateEffective checks requirements that depend on the backend selected
// after environment overrides. A lexical or sqlite override may recover a file
// that selected hybrid without an endpoint.
func validateEffective(cfg Config) error {
	if cfg.Search.Backend != "hybrid" {
		return nil
	}
	if strings.TrimSpace(cfg.Embedding.BaseURL) == "" {
		return errors.New("hybrid search requires embedding.baseURL")
	}
	if strings.TrimSpace(cfg.Embedding.Model) == "" || cfg.Embedding.Dimensions <= 0 {
		return errors.New("hybrid search requires embedding.model and a positive embedding.dimensions")
	}
	return nil
}

func validateAPIKeyEnv(field, name string) error {
	if name == "" {
		return nil
	}
	if !envNamePattern.MatchString(name) {
		return fmt.Errorf("%s must name an environment variable, not a key or expression", field)
	}
	return nil
}

func validateBaseURL(field, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL", field)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s must use http or https", field)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", field)
	}
	if parsed.User != nil {
		return fmt.Errorf("%s must not contain credentials; name a key with apiKeyEnv", field)
	}
	return nil
}

// Summary returns printable lines with secret values shown as set or unset.
func (c Config) Summary(getenv func(string) string) []string {
	secret := func(name string) string {
		name = strings.TrimSpace(name)
		switch {
		case name == "":
			return "not set"
		case !envNamePattern.MatchString(name):
			return "invalid"
		case strings.TrimSpace(getenv(name)) != "":
			return "set"
		default:
			return "unset"
		}
	}
	orUnset := func(value string) string {
		if strings.TrimSpace(value) == "" {
			return "not set"
		}
		return value
	}
	orURL := func(value string) string {
		if strings.TrimSpace(value) == "" {
			return "not set"
		}
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return "configured"
		}
		return parsed.Scheme + "://" + parsed.Host
	}
	command := "not configured"
	if len(c.Generation.Command) > 0 {
		command = fmt.Sprintf("configured (%d arguments)", len(c.Generation.Command))
	}
	return []string{
		"search.backend: " + c.Search.Backend,
		"embedding.baseURL: " + orURL(c.Embedding.BaseURL),
		"embedding.model: " + orUnset(c.Embedding.Model),
		fmt.Sprintf("embedding.dimensions: %d", c.Embedding.Dimensions),
		"embedding.apiKeyEnv: " + secret(c.Embedding.APIKeyEnv),
		"generation.mode: " + c.Generation.Mode,
		"generation.command: " + command,
		"generation.http.baseURL: " + orURL(c.Generation.HTTP.BaseURL),
		"generation.http.model: " + orUnset(c.Generation.HTTP.Model),
		"generation.http.apiKeyEnv: " + secret(c.Generation.HTTP.APIKeyEnv),
		fmt.Sprintf("generation.timeoutSeconds: %d", c.Generation.TimeoutSeconds),
	}
}
