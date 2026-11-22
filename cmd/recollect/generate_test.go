package main

// audit: behavior - command and HTTP generators run the configured provider and map failures to stable job codes.
// audit: regression - a generator could hang, recurse, leak unbounded stderr, or surface a raw provider error.
// audit: coverage gap - generation lived only in the OpenCode adapter.
// audit: seam - tests run the test binary as a helper command and an httptest server through buildGenerator.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/config"
)

// TestGeneratorHelper is the child entrypoint for the command generator tests.
func TestGeneratorHelper(t *testing.T) {
	if os.Getenv("GO_WANT_GENERATOR_HELPER") != "1" {
		return
	}
	if os.Getenv("RECOLLECT_NESTED") != "1" {
		os.Exit(3)
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(4)
	}
	fmt.Fprint(os.Stdout, "reply:"+string(data))
	os.Exit(0)
}

// TestGeneratorSleepHelper blocks so the parent can observe a timeout.
func TestGeneratorSleepHelper(t *testing.T) {
	if os.Getenv("GO_WANT_GENERATOR_SLEEP") != "1" {
		return
	}
	time.Sleep(5 * time.Second)
	os.Exit(0)
}

// TestGeneratorFailHelper writes stderr and exits non-zero.
func TestGeneratorFailHelper(t *testing.T) {
	if os.Getenv("GO_WANT_GENERATOR_FAIL") != "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "provider boom")
	os.Exit(2)
}

// TestGeneratorGrandchildHelper leaves a grandchild holding stdout, then
// sleeps past the parent timeout.
func TestGeneratorGrandchildHelper(t *testing.T) {
	if os.Getenv("GO_WANT_GENERATOR_GRANDCHILD") != "1" {
		return
	}
	command := exec.Command(os.Args[0], "-test.run=TestGeneratorSleepHelper", "--")
	command.Env = append(os.Environ(), "GO_WANT_GENERATOR_SLEEP=1")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		os.Exit(5)
	}
	time.Sleep(5 * time.Second)
	os.Exit(0)
}

func helperCommand(testName string) []string {
	return []string{os.Args[0], "-test.run=" + testName, "--"}
}

func TestCommandGenerator(t *testing.T) {
	t.Setenv("GO_WANT_GENERATOR_HELPER", "1")
	generator, err := buildGenerator(config.Generation{
		Mode:           "command",
		Command:        helperCommand("TestGeneratorHelper"),
		TimeoutSeconds: 5,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got, err := generator(context.Background(), "hello")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got != "reply:hello" {
		t.Fatalf("output = %q, want reply:hello", got)
	}
}

func TestCommandGeneratorTimeout(t *testing.T) {
	t.Setenv("GO_WANT_GENERATOR_SLEEP", "1")
	generator, err := buildGenerator(config.Generation{
		Mode:           "command",
		Command:        helperCommand("TestGeneratorSleepHelper"),
		TimeoutSeconds: 1,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	_, err = generator(context.Background(), "hello")
	if err == nil || err.Error() != "generation_timeout" {
		t.Fatalf("err = %v, want generation_timeout", err)
	}
}

func TestCommandGeneratorFailureLogsStderr(t *testing.T) {
	t.Setenv("GO_WANT_GENERATOR_FAIL", "1")
	var log bytes.Buffer
	generator, err := buildGenerator(config.Generation{
		Mode:           "command",
		Command:        helperCommand("TestGeneratorFailHelper"),
		TimeoutSeconds: 5,
	}, &log)
	if err != nil {
		t.Fatal(err)
	}
	_, err = generator(context.Background(), "hello")
	if err == nil || err.Error() != "generation_failed" {
		t.Fatalf("err = %v, want generation_failed", err)
	}
	if !strings.Contains(log.String(), "provider boom") {
		t.Fatalf("stderr log = %q", log.String())
	}
}

func TestCommandGeneratorBoundedByGrandchildPipe(t *testing.T) {
	t.Setenv("GO_WANT_GENERATOR_GRANDCHILD", "1")
	generator, err := buildGenerator(config.Generation{
		Mode:           "command",
		Command:        helperCommand("TestGeneratorGrandchildHelper"),
		TimeoutSeconds: 1,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = generator(context.Background(), "hello")
	elapsed := time.Since(start)
	if err == nil || err.Error() != "generation_timeout" {
		t.Fatalf("err = %v, want generation_timeout", err)
	}
	if elapsed > 4*time.Second {
		t.Fatalf("generator blocked for %s", elapsed)
	}
}

func TestHTTPGenerator(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.Model != "test" || len(body.Messages) != 1 || body.Messages[0].Content != "hello" {
			t.Errorf("body = %+v", body)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"world"}}]}`)
	}))
	defer server.Close()

	generator, err := buildGenerator(config.Generation{
		Mode:           "http",
		HTTP:           config.HTTP{BaseURL: server.URL, Model: "test"},
		TimeoutSeconds: 5,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	got, err := generator(context.Background(), "hello")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got != "world" {
		t.Fatalf("output = %q, want world", got)
	}
}

func TestHTTPGeneratorStatusFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	generator, err := buildGenerator(config.Generation{
		Mode:           "http",
		HTTP:           config.HTTP{BaseURL: server.URL, Model: "test"},
		TimeoutSeconds: 5,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	_, err = generator(context.Background(), "hello")
	if err == nil || err.Error() != "generation_failed" {
		t.Fatalf("err = %v, want generation_failed", err)
	}
}

func TestHTTPGeneratorBodyTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	generator, err := buildGenerator(config.Generation{
		Mode:           "http",
		HTTP:           config.HTTP{BaseURL: server.URL, Model: "test"},
		TimeoutSeconds: 1,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	_, err = generator(context.Background(), "hello")
	if err == nil || err.Error() != "generation_timeout" {
		t.Fatalf("err = %v, want generation_timeout", err)
	}
}

func TestHTTPGeneratorBodyCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	generator, err := buildGenerator(config.Generation{
		Mode:           "http",
		HTTP:           config.HTTP{BaseURL: server.URL, Model: "test"},
		TimeoutSeconds: 5,
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_, err = generator(ctx, "hello")
	if err == nil || err.Error() != "cancelled" {
		t.Fatalf("err = %v, want cancelled", err)
	}
}

func TestBuildGeneratorOff(t *testing.T) {
	generator, err := buildGenerator(config.Generation{Mode: "off"}, io.Discard)
	if err != nil || generator != nil {
		t.Fatalf("generator = %v, err = %v", generator, err)
	}
}

func TestBuildGeneratorRequiresCommand(t *testing.T) {
	if _, err := buildGenerator(config.Generation{Mode: "command"}, io.Discard); err == nil {
		t.Fatal("command mode without a command accepted")
	}
	if _, err := buildGenerator(config.Generation{Mode: "command", Command: []string{"  "}}, io.Discard); err == nil {
		t.Fatal("blank executable accepted")
	}
}
