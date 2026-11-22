package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dkmnx/recollect/internal/config"
	"github.com/dkmnx/recollect/internal/jobs"
)

const (
	generateMaxResponseBytes = 16 << 20
	generateStderrLimit      = 4 << 10
	generateWaitDelay        = 5 * time.Second
)

func waitDelay(timeout time.Duration) time.Duration {
	if timeout < generateWaitDelay {
		return timeout
	}
	return generateWaitDelay
}

// buildGenerator returns the configured generator, or nil when generation is
// off.
func buildGenerator(cfg config.Generation, log io.Writer) (jobs.Generator, error) {
	if log == nil {
		log = io.Discard
	}
	switch cfg.Mode {
	case "off":
		return nil, nil
	case "command":
		if len(cfg.Command) == 0 || strings.TrimSpace(cfg.Command[0]) == "" {
			return nil, errors.New("generation.command[0] must name an executable")
		}
		return commandGenerator(cfg, log), nil
	case "http":
		if strings.TrimSpace(cfg.HTTP.BaseURL) == "" || strings.TrimSpace(cfg.HTTP.Model) == "" {
			return nil, errors.New("generation.http.baseURL and generation.http.model are required")
		}
		return httpGenerator(cfg), nil
	default:
		return nil, fmt.Errorf("unknown generation mode %q", cfg.Mode)
	}
}

func commandGenerator(cfg config.Generation, log io.Writer) jobs.Generator {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	return func(ctx context.Context, prompt string) (string, error) {
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		command := exec.CommandContext(runCtx, cfg.Command[0], cfg.Command[1:]...)
		configureProcess(command)
		command.WaitDelay = waitDelay(timeout)
		command.Stdin = strings.NewReader(prompt)
		command.Env = append(os.Environ(), "RECOLLECT_NESTED=1")
		stdout := &cappedBuffer{max: generateMaxResponseBytes}
		stderr := &cappedBuffer{max: generateStderrLimit}
		command.Stdout = stdout
		command.Stderr = stderr
		if err := command.Run(); err != nil {
			if runCtx.Err() != nil {
				killProcessTree(command)
			}
			if runCtx.Err() == context.DeadlineExceeded {
				return "", errors.New("generation_timeout")
			}
			if runCtx.Err() == context.Canceled {
				return "", errors.New("cancelled")
			}
			if text := strings.TrimSpace(stderr.String()); text != "" {
				fmt.Fprintf(log, "recollect: generator stderr: %s\n", text)
			}
			return "", errors.New("generation_failed")
		}
		if stdout.truncated {
			return "", errors.New("generation_failed")
		}
		return stdout.String(), nil
	}
}

func httpGenerator(cfg config.Generation) jobs.Generator {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.HTTP.BaseURL), "/")
	model := strings.TrimSpace(cfg.HTTP.Model)
	apiKey := ""
	if cfg.HTTP.APIKeyEnv != "" {
		apiKey = os.Getenv(cfg.HTTP.APIKeyEnv)
	}
	client := &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}
	return func(ctx context.Context, prompt string) (string, error) {
		payload, err := json.Marshal(map[string]any{
			"model":    model,
			"messages": []map[string]string{{"role": "user", "content": prompt}},
		})
		if err != nil {
			return "", errors.New("generation_failed")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			return "", errors.New("generation_failed")
		}
		request.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			request.Header.Set("Authorization", "Bearer "+apiKey)
		}
		response, err := client.Do(request)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return "", errors.New("generation_timeout")
			}
			return "", classifyContext(ctx)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, generateMaxResponseBytes+1))
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return "", errors.New("generation_timeout")
			}
			return "", classifyContext(ctx)
		}
		if len(data) > generateMaxResponseBytes || response.StatusCode != http.StatusOK {
			return "", errors.New("generation_failed")
		}
		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &result); err != nil || len(result.Choices) == 0 {
			return "", errors.New("generation_failed")
		}
		return result.Choices[0].Message.Content, nil
	}
}

func classifyContext(ctx context.Context) error {
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return errors.New("generation_timeout")
	case context.Canceled:
		return errors.New("cancelled")
	default:
		return errors.New("generation_failed")
	}
}

type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := b.max - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
			b.truncated = true
		} else {
			b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }
