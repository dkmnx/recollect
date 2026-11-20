package memory_test

// audit: behavior contract preserved from xi; regression cases retained.
// audit: coverage unchanged; no test-only seam introduced.

// Model replies are an external boundary: the capture parsers repair the
// invalid escape sequences LLMs emit (e.g. \( \) \x) before unmarshaling,
// while leaving valid JSON escapes untouched.

import (
	"github.com/dkmnx/recollect/internal/memory"
	"strings"
	"testing"
)

func TestParseCaptureDigestRepairsInvalidEscapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"escaped paren", `{"topic":"x","summary":"see \(foo\) here"}`, `see \(foo\) here`},
		{"backslash x", `{"topic":"x","summary":"regex \x match"}`, `regex \x match`},
		{"tilde", `{"topic":"x","summary":"about \~home"}`, `about \~home`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, summary, err := memory.ParseCaptureDigest(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if summary != tc.want {
				t.Fatalf("summary = %q, want %q", summary, tc.want)
			}
		})
	}
}

func TestParseCaptureDigestKeepsValidEscapes(t *testing.T) {
	_, summary, err := memory.ParseCaptureDigest(`{"topic":"x","summary":"line\nnext \\ \"q\" \u0041"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := "line\nnext \\ \"q\" A"
	if summary != want {
		t.Fatalf("summary = %q, want %q", summary, want)
	}
}

func TestParseCaptureResultRepairsInvalidEscapes(t *testing.T) {
	raw := `{"ops":[{"action":"add","type":"fact","key":"esc","summary":"repaired \) op","content":"cmd \x"}],"reason":""}`
	result, err := memory.ParseCaptureResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Ops) != 1 {
		t.Fatalf("ops = %+v", result.Ops)
	}
	if result.Ops[0].Summary != `repaired \) op` || result.Ops[0].Content != `cmd \x` {
		t.Fatalf("op = %+v", result.Ops[0])
	}
}

// TestExtractJSONObjectDiagnosesShape pins the no-JSON diagnosis: the
// shapes an unparseable capture reply takes must be distinguishable, and the
// error must use a stable code and carry no model text.
func TestExtractJSONObjectDiagnosesShape(t *testing.T) {
	long := strings.Repeat("a", 200) + "TAILMARKER"
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty reply", raw: "  \n ", want: "empty"},
		{name: "prose", raw: "Nothing worth saving this turn.", want: "no JSON object"},
		{name: "unclosed object", raw: `{"ops":[{"action":"add"`, want: "unclosed"},
		{name: "secret in prose", raw: "summary: api_key = abcdef0123456789", want: "no JSON object"},
		{name: "long prose", raw: long, want: "no JSON object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := memory.ExtractJSONObjectForTest(tc.raw)
			if err == nil {
				t.Fatalf("memory.ExtractJSONObjectForTest(%q) = %q, want an error", tc.raw, got)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("err = %q, want it to name %q", msg, tc.want)
			}
			if !strings.Contains(msg, "INVALID_REPLY") {
				t.Fatalf("err = %q, want a stable reply code", msg)
			}
			for _, forbidden := range []string{"Nothing worth saving", "abcdef0123456789", "TAILMARKER"} {
				if strings.Contains(msg, forbidden) {
					t.Fatalf("err = %q, carried model text %q", msg, forbidden)
				}
			}
		})
	}
}

func TestRepairJSONEscapes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a":"b"}`, `{"a":"b"}`},
		{`{"a":"x \) y"}`, `{"a":"x \\) y"}`},
		{`{"a":"x \\ y"}`, `{"a":"x \\ y"}`},
		{`{"a":"x \u0041 y"}`, `{"a":"x \u0041 y"}`},
		{`{"a":"x \uZZZZ y"}`, `{"a":"x \\uZZZZ y"}`},
		{`{"a":"quote \" and \) tail"}`, `{"a":"quote \" and \\) tail"}`},
		{`{"a":"trailing \`, `{"a":"trailing \\`},
	}
	for _, tc := range cases {
		if got := memory.RepairJSONEscapesForTest(tc.in); got != tc.want {
			t.Errorf("memory.RepairJSONEscapesForTest(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseCaptureResultUnquotedKeys(t *testing.T) {
	// The reported failure: a consolidation reply with bare keys —
	// "invalid character 'r' looking for beginning of value".
	result, err := memory.ParseCaptureResult("{reason: nothing worth saving}")
	if err != nil {
		t.Fatalf("unquoted-key reply rejected: %v", err)
	}
	if result.Reason != "nothing worth saving" || len(result.Ops) != 0 {
		t.Fatalf("result = %+v", result)
	}
	result, err = memory.ParseCaptureResult("{ops: [{action: add, summary: build is green, content: run make check}], reason: captured one}")
	if err != nil {
		t.Fatalf("nested unquoted reply rejected: %v", err)
	}
	if len(result.Ops) != 1 || result.Ops[0].Summary != "build is green" || result.Ops[0].Content != "run make check" {
		t.Fatalf("ops = %+v", result.Ops)
	}
}

func TestParseCaptureResultNearJSONWarts(t *testing.T) {
	cases := []string{
		"{'ops': [], 'reason': 'none'}",
		"{“ops”: [], “reason”: “curly quotes”}",
		`{"ops": [], "reason": "trailing",}`,
		"Here you go:\n{ops: [], reason: skipped}\nHope that helps!",
		"{ops: [{action: drop, key: stale-fact}], reason: r}",
		"{reason: why not, ops: [{action: add, summary: forty two, content: n}]}",
	}
	for _, raw := range cases {
		result, err := memory.ParseCaptureResult(raw)
		if err != nil {
			t.Errorf("reply %q rejected: %v", raw, err)
			continue
		}
		_ = result
	}
}

func TestParseCaptureResultStrictStillWins(t *testing.T) {
	result, err := memory.ParseCaptureResult(`{"ops":[{"action":"add","type":"feedback","key":"k","summary":"s","content":"c"}],"reason":"ok"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Ops) != 1 || result.Ops[0].Type != memory.FactTypeFeedback || result.Reason != "ok" {
		t.Fatalf("result = %+v", result)
	}
}

func TestParseCaptureResultErrorUsesStableCode(t *testing.T) {
	_, err := memory.ParseCaptureResult("plain prose only, no braces at all")
	if err == nil || !strings.Contains(err.Error(), `carries no JSON object`) {
		t.Fatalf("err = %v", err)
	}
	_, err = memory.ParseCaptureResult("{ops:}")
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseCaptureDigestNearJSON(t *testing.T) {
	topic, summary, err := memory.ParseCaptureDigest("{topic: capture wiring, summary: landed the run-end trigger with tests}")
	if err != nil {
		t.Fatalf("digest rejected: %v", err)
	}
	if topic != "capture wiring" || summary != "landed the run-end trigger with tests" {
		t.Fatalf("digest = %q / %q", topic, summary)
	}
	_, _, err = memory.ParseCaptureDigest("{topic: t}")
	if err == nil || !strings.Contains(err.Error(), "carries no summary") || !strings.Contains(err.Error(), "INVALID_REPLY") {
		t.Fatalf("missing-summary error = %v", err)
	}
}

func TestParseCaptureResultLinks(t *testing.T) {
	raw := `{"ops":[` +
		`{"action":"add","key":"deploy-flow","summary":"deploys","content":"c","links":["redis-tuning","ghost-note"]},` +
		`{"action":"update","key":"redis-tuning","summary":"cache","content":"c2","links":"deploy-flow, Notes Cache, deploy-flow"},` +
		`{"action":"add","key":"bare","summary":"no links survive garbage","content":"c3","links":42}` +
		`],"reason":""}`
	result, err := memory.ParseCaptureResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Ops) != 3 {
		t.Fatalf("ops = %d", len(result.Ops))
	}
	if got := result.Ops[0].Links; len(got) != 2 || got[0] != "redis-tuning" || got[1] != "ghost-note" {
		t.Fatalf("array links = %v", got)
	}
	if got := result.Ops[1].Links; len(got) != 2 || got[0] != "deploy-flow" || got[1] != "notes-cache" {
		t.Fatalf("string links = %v", got)
	}
	if got := result.Ops[2].Links; len(got) != 0 {
		t.Fatalf("garbage links = %v, want dropped", got)
	}
}
