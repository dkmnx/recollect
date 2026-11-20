// capture_parse.go — parsing the capture model replies: a tolerant JSON
// extractor, consolidation ops with validation, and the digest reply. Bad
// ops are dropped so one malformed field cannot lose the rest of a capture.

package memory

import (
	"encoding/json"
	"strings"
)

// CaptureOp is one memory operation parsed from a consolidation reply.
type CaptureOp struct {
	Action  string     `json:"action"`
	Type    string     `json:"type"`
	Scene   string     `json:"scene,omitempty"`
	Key     string     `json:"key"`
	Summary string     `json:"summary"`
	Content string     `json:"content"`
	Links   LinksField `json:"links,omitempty"`
}

// LinksField tolerates the two shapes a model writes links in: a JSON
// array of names or one comma-separated string. Anything else decodes as
// absent: links are auxiliary, so garbage there must not fail an op that
// would otherwise land.
type LinksField []string

func (f *LinksField) UnmarshalJSON(data []byte) error {
	var names []string
	if err := json.Unmarshal(data, &names); err == nil {
		*f = names
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*f = strings.Split(text, ",")
		return nil
	}
	*f = nil
	return nil
}

// CaptureResult is a parsed consolidation reply. Dropped counts the ops
// normalization rejected (unknown action, missing key or summary): they
// are capture's tolerance rule — one malformed field must not lose the
// rest — and the count lets a caller report what the reply tried.
type CaptureResult struct {
	Ops     []CaptureOp
	Reason  string
	Dropped int
}

// ParseCaptureResult parses the model's consolidation JSON, tolerating code
// fences, surrounding prose, and near-JSON (unquoted keys, single-quoted or
// curly-quoted strings, trailing commas). Invalid ops are dropped;
// unparseable JSON is a stable coded error with no model text.
func ParseCaptureResult(raw string) (CaptureResult, error) {
	payload, err := extractJSONObject(raw)
	if err != nil {
		return CaptureResult{}, err
	}
	var envelope struct {
		Ops    []CaptureOp `json:"ops"`
		Reason string      `json:"reason"`
	}
	if err := decodeCaptureJSON(payload, &envelope); err != nil {
		return CaptureResult{}, Errorf(CodeInvalidReply, "capture reply is not valid JSON")
	}
	result := CaptureResult{Reason: strings.TrimSpace(envelope.Reason)}
	for _, op := range envelope.Ops {
		normalized, ok := normalizeCaptureOp(op)
		if !ok {
			result.Dropped++
			continue
		}
		result.Ops = append(result.Ops, normalized)
	}
	return result, nil
}

// ParseCaptureDigest parses the model's digest JSON.
func ParseCaptureDigest(raw string) (topic, summary string, err error) {
	payload, err := extractJSONObject(raw)
	if err != nil {
		return "", "", err
	}
	var envelope struct {
		Topic   string `json:"topic"`
		Summary string `json:"summary"`
	}
	if err := decodeCaptureJSON(payload, &envelope); err != nil {
		return "", "", Errorf(CodeInvalidReply, "digest reply is not valid JSON")
	}
	summary = strings.TrimSpace(envelope.Summary)
	if summary == "" {
		return "", "", Errorf(CodeInvalidReply, "digest reply carries no summary")
	}
	return CollapseLine(envelope.Topic), summary, nil
}

// decodeCaptureJSON decodes a capture reply strictly first, then retries
// once through loosenJSON. The retry only ever runs after the strict decode
// failed, so a wrong guess there cannot corrupt a valid reply.
func decodeCaptureJSON(payload string, v any) error {
	strict := repairJSONEscapes(payload)
	if err := json.Unmarshal([]byte(strict), v); err == nil {
		return nil
	} else if err := json.Unmarshal([]byte(loosenJSON(strict)), v); err == nil {
		return nil
	} else {
		return err
	}
}

// normalizeCaptureOp validates one op and normalizes its fields. Unknown
// non-empty fact types survive in lowercase form; only empty defaults to
// the shared project type.
func normalizeCaptureOp(op CaptureOp) (CaptureOp, bool) {
	op.Action = strings.ToLower(strings.TrimSpace(op.Action))
	op.Type = NormalizeFactType(op.Type)
	op.Scene = NormalizeScene(op.Scene)
	op.Key = FactSlug(op.Key)
	op.Summary = CollapseLine(op.Summary)
	op.Content = strings.TrimSpace(op.Content)
	op.Links = normalizeLinks(op.Key, op.Links)
	switch op.Action {
	case "add", "update":
		if op.Summary == "" {
			return CaptureOp{}, false
		}
	case "correct":
		if op.Key == "" || op.Summary == "" {
			return CaptureOp{}, false
		}
	case "demote":
		// A demote names the fact to move; a summary alone cannot address it.
		if op.Key == "" {
			return CaptureOp{}, false
		}
	case "drop":
		if op.Key == "" {
			return CaptureOp{}, false
		}
	default:
		return CaptureOp{}, false
	}
	return op, true
}

// NormalizeFactType trims and lowercases a fact type. Known and unknown types
// are preserved: a writer must not silently relabel an existing or imported
// type. Only an empty type defaults to project.
func NormalizeFactType(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return FactTypeProject
	}
	return normalized
}

// extractJSONObject returns the first {...} span, tolerating code fences and
// surrounding prose. A reply with no usable object returns a stable error
// code and a fixed diagnostic. Model text never enters durable state.
func extractJSONObject(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", Errorf(CodeInvalidReply, "capture reply was empty")
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	switch {
	case start < 0:
		return "", Errorf(CodeInvalidReply, "capture reply carries no JSON object")
	case end <= start:
		return "", Errorf(CodeInvalidReply, "capture reply has an unclosed JSON object")
	}
	return text[start : end+1], nil
}

// repairJSONEscapes rewrites escape sequences JSON does not allow (\)
// \( \x, a \u without four hex digits) as literal backslashes. Model
// replies routinely over-escape prose (LaTeX-style parens, regexes), and a
// strict decoder rejects the whole object over one such sequence. Valid
// escapes, including escaped quotes and backslashes, are preserved; the
// scan only rewrites inside string literals.
func repairJSONEscapes(payload string) string {
	var b strings.Builder
	b.Grow(len(payload))
	inString := false
	for i := 0; i < len(payload); i++ {
		c := payload[i]
		if !inString {
			b.WriteByte(c)
			if c == '"' {
				inString = true
			}
			continue
		}
		switch {
		case c == '\\':
			if i+1 < len(payload) && validJSONEscape(payload, i+1) {
				b.WriteByte('\\')
				b.WriteByte(payload[i+1])
				i++
				continue
			}
			// Not a valid escape: keep the backslash literally and let the
			// next byte fall through to the ordinary copy.
			b.WriteString(`\\`)
		case c == '"':
			inString = false
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// validJSONEscape reports whether the byte after a backslash starts a valid
// JSON escape: one of the single-character escapes, or \u followed by four
// hex digits.
func validJSONEscape(payload string, j int) bool {
	switch payload[j] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	case 'u':
		return j+4 < len(payload) && isHex4(payload[j+1:j+5])
	}
	return false
}

// isHex4 reports whether s is exactly four hexadecimal digits.
func isHex4(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// loosenJSON rewrites the near-JSON a capture model emits — unquoted keys
// and bare values, single-quoted or curly-quoted strings, trailing commas —
// into strict JSON. The scan is byte-level, which is safe because UTF-8
// continuation bytes never collide with the ASCII structural characters.
func loosenJSON(payload string) string {
	payload = strings.NewReplacer("“", `"`, "”", `"`, "‘", "'", "’", "'").Replace(payload)
	buf := make([]byte, 0, len(payload)+16)
	inString := false
	singleQuoted := false
	var stack []byte
	expectKey := false
	expectValue := false
	for i := 0; i < len(payload); i++ {
		c := payload[i]
		if inString {
			if !singleQuoted && c == '\\' && i+1 < len(payload) {
				buf = append(buf, c, payload[i+1])
				i++
				continue
			}
			if c == '"' && !singleQuoted || c == '\'' && singleQuoted {
				inString, singleQuoted = false, false
				buf = append(buf, '"')
				continue
			}
			buf = append(buf, c)
			continue
		}
		switch {
		case c == '"' || c == '\'':
			inString, singleQuoted = true, c == '\''
			expectKey, expectValue = false, false
			buf = append(buf, '"')
		case c == '{' || c == '[':
			stack = append(stack, c)
			expectKey = c == '{'
			expectValue = false
			buf = append(buf, c)
		case c == '}' || c == ']':
			if n := len(stack); n > 0 {
				stack = stack[:n-1]
			}
			buf = trimTrailingComma(buf)
			expectKey, expectValue = false, false
			buf = append(buf, c)
		case c == ',':
			expectKey = len(stack) > 0 && stack[len(stack)-1] == '{'
			expectValue = false
			buf = append(buf, c)
		case c == ':':
			expectValue = true
			expectKey = false
			buf = append(buf, c)
		case (expectKey || expectValue) && isWordByte(c):
			start := i
			end := start + 1
			if expectKey {
				for end < len(payload) && isWordByte(payload[end]) {
					end++
				}
				j := end
				for j < len(payload) && isSpaceByte(payload[j]) {
					j++
				}
				if j < len(payload) && payload[j] == ':' {
					buf = append(buf, '"')
					buf = append(buf, payload[start:end]...)
					buf = append(buf, '"')
				} else {
					buf = append(buf, payload[start:end]...)
				}
				i = end - 1
				continue
			}
			// A bare value: read to the next structural delimiter and
			// quote the span unless it is a JSON literal already.
			for end < len(payload) {
				next := payload[end]
				if next == ',' || next == '}' || next == ']' || next == '\n' || next == '"' {
					break
				}
				end++
			}
			value := strings.TrimSpace(payload[start:end])
			if isJSONLiteral(value) {
				buf = append(buf, value...)
			} else {
				buf = append(buf, '"')
				buf = append(buf, strings.ReplaceAll(value, `"`, `\\"`)...)
				buf = append(buf, '"')
			}
			expectValue = false
			i = end - 1
		default:
			buf = append(buf, c)
		}
	}
	return string(buf)
}

// trimTrailingComma drops a trailing comma (and whitespace before it) so
// `{...,}` and `[...,]` decode.
func trimTrailingComma(buf []byte) []byte {
	end := len(buf)
	for end > 0 && isSpaceByte(buf[end-1]) {
		end--
	}
	if end > 0 && buf[end-1] == ',' {
		buf = buf[:end-1]
	}
	return buf
}

// isWordByte reports whether c can start or continue a bare JSON word.
func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$'
}

// isSpaceByte reports whether c is JSON-insignificant whitespace.
func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// isJSONLiteral reports whether value is already a valid bare JSON scalar:
// true, false, null, or a number.
func isJSONLiteral(value string) bool {
	switch value {
	case "true", "false", "null":
		return true
	}
	if value == "" {
		return false
	}
	digits := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= '0' && c <= '9':
			digits = true
		case c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E':
		default:
			return false
		}
	}
	return digits
}
