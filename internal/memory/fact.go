// fact.go — the fact-file format: one fact per markdown file with YAML
// frontmatter, conforming to docs/memory.md (format version 1,
// ZCode's project memory layout). Parsing is tolerant and never fails: a
// file without frontmatter is a valid fact whose name comes from its file
// name and whose description is its first body line.

package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// The shared fact-type vocabulary. Unknown types are preserved as read and
// never rejected; writers default an empty type to project. Experience
// marks the second layer: a reusable approach whose links name the facts it
// was learned from.
const (
	FactTypeUser       = "user"
	FactTypeFeedback   = "feedback"
	FactTypeProject    = "project"
	FactTypeReference  = "reference"
	FactTypeExperience = "experience"
)

// The scene vocabulary: the domain a fact belongs to, adapted from
// Hy-Memory's task scenes. An empty scene is unscoped: the fact matches
// every scene-scoped search, so a missing tag can never hide knowledge.
const (
	FactSceneChat    = "chat"
	FactSceneWorking = "working"
	FactSceneCoding  = "coding"
)

// NormalizeScene maps the scene vocabulary onto its canonical lower-case
// form and blanks anything else, so a scene-scoped search can filter on
// exact values and a hand-edited junk tag cannot leak into a rewrite.
func NormalizeScene(raw string) string {
	switch scene := strings.ToLower(strings.TrimSpace(raw)); scene {
	case FactSceneChat, FactSceneWorking, FactSceneCoding:
		return scene
	default:
		return ""
	}
}

// maxFactNameBytes bounds a fact name.
const maxFactNameBytes = 48

// maxFactLinks bounds a fact's link list.
const maxFactLinks = 8

// FactNodeType is the fixed node_type every writer stamps, matching the
// ZCode files the format conforms to.
const FactNodeType = "memory"

// Fact is one memory record: a markdown file with frontmatter.
type Fact struct {
	Name        string // slug; the file's base name and the stable address
	Description string // one line; the index hook and recall summary
	Type        string // user|feedback|project|reference|experience, preserved as read
	// Scene is the fact's domain: chat|working|coding, or "" when
	// unclassified. Unscoped facts match every scene-scoped search.
	Scene           string
	OriginSessionID string // optional provenance, stamped by capture
	// CapturedOn is the creation date (YYYY-MM-DD). Append stamps it and
	// replace keeps it, so a fact records when the knowledge was learned
	// however often it is corrected.
	CapturedOn string
	// Links names the facts this one continues, updates, or contradicts.
	Links []string
	Body  string
	// Path is the slash-separated directory of the file relative to the
	// project directory: "" at the root, "topics/<topic>" inside a topic.
	Path string
	// ModTime is the file's modification time; zero for facts not read from
	// disk. It orders the index, newest first.
	ModTime time.Time
}

// FileName is the fact's file name, including the markdown extension.
func (f Fact) FileName() string { return f.Name + ".md" }

// RelPath is the file's path relative to the project directory.
func (f Fact) RelPath() string {
	if f.Path == "" {
		return f.FileName()
	}
	return f.Path + "/" + f.FileName()
}

// ParseFact parses fact-file content. Unknown frontmatter keys are ignored;
// folded (indented continuation) description lines are joined with single
// spaces.
func ParseFact(content string) Fact {
	meta, body, ok := splitFrontmatter(NormalizeNewlines(strings.TrimPrefix(content, "\ufeff")))
	fact := Fact{Body: strings.Trim(body, "\n")}
	if !ok {
		return fact
	}
	fact.Name = FactSlug(meta["name"])
	fact.Description = CollapseLine(meta["description"])
	fact.Type = strings.TrimSpace(meta["type"])
	fact.Scene = NormalizeScene(meta["scene"])
	fact.OriginSessionID = strings.TrimSpace(meta["originSessionId"])
	fact.CapturedOn = strings.TrimSpace(meta["capturedOn"])
	fact.Links = normalizeLinks(fact.Name, strings.Split(meta["links"], ","))
	return fact
}

// FormatFact renders the canonical fact file. The description is collapsed
// to one line; an empty body emits the frontmatter block only.
func FormatFact(fact Fact) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + fact.Name + "\n")
	b.WriteString("description: " + CollapseLine(fact.Description) + "\n")
	if date := strings.TrimSpace(fact.CapturedOn); date != "" {
		b.WriteString("capturedOn: " + date + "\n")
	}
	if links := normalizeLinks(fact.Name, fact.Links); len(links) > 0 {
		b.WriteString("links: " + strings.Join(links, ", ") + "\n")
	}
	b.WriteString("metadata:\n")
	b.WriteString("  type: " + strings.TrimSpace(fact.Type) + "\n")
	b.WriteString("  node_type: " + FactNodeType + "\n")
	if scene := NormalizeScene(fact.Scene); scene != "" {
		b.WriteString("  scene: " + scene + "\n")
	}
	if session := strings.TrimSpace(fact.OriginSessionID); session != "" {
		b.WriteString("  originSessionId: " + session + "\n")
	}
	b.WriteString("---\n")
	if body := strings.Trim(fact.Body, "\n"); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	return b.String()
}

// FactSlug slugs a fact name: lowercase [a-z0-9] runs joined by single
// dashes, capped at maxFactNameBytes.
func FactSlug(text string) string { return SlugKey(text, maxFactNameBytes) }

// FallbackName derives a stable name for a fact whose description slugs to
// nothing (non-ASCII descriptions): the first 8 hex characters of
// sha256(description), so distinct descriptions stay distinct.
func (f Fact) FallbackName() string {
	sum := sha256.Sum256([]byte(f.Description))
	return hex.EncodeToString(sum[:])[:8]
}

// prepareFact normalizes a fact for writing: the description collapses to
// one line, an empty type defaults to project, and the name slugs from the
// given name or the description, falling back to a stable hash of the
// description when nothing usable remains (non-ASCII descriptions).
// PrepareForWrite applies the store's write-time normalization to a fact.
// Adapters that route writes through the engine use it to keep stored facts
// identical to those written by the store directly.
func PrepareForWrite(fact Fact) Fact { return prepareFact(fact) }

func prepareFact(fact Fact) Fact {
	fact.Description = StripPrivateContent(CollapseLine(fact.Description))
	fact.Type = strings.TrimSpace(fact.Type)
	if fact.Type == "" {
		fact.Type = FactTypeProject
	}
	fact.Scene = NormalizeScene(fact.Scene)
	fact.Name = FactSlug(fact.Name)
	if fact.Name == "" {
		fact.Name = FactSlug(fact.Description)
	}
	if fact.Name == "" {
		fact.Name = fact.FallbackName()
	}
	fact.OriginSessionID = strings.TrimSpace(fact.OriginSessionID)
	fact.Links = normalizeLinks(fact.Name, fact.Links)
	fact.Body = StripPrivateContent(strings.Trim(fact.Body, "\n"))
	return fact
}

// normalizeLinks slugs every entry and drops empty, self, and duplicate
// names, so a fact's links stay valid addresses no matter what a model or
// a hand editor wrote. Parse and every writer share it.
func normalizeLinks(own string, raw []string) []string {
	var links []string
	seen := map[string]bool{}
	for _, entry := range raw {
		slug := FactSlug(entry)
		if slug == "" || slug == own || seen[slug] {
			continue
		}
		seen[slug] = true
		links = append(links, slug)
		if len(links) == maxFactLinks {
			break
		}
	}
	return links
}

// splitFrontmatter splits a leading `---` block from the body. It reports
// whether a terminated block was found; meta holds top-level keys and the
// keys inside the metadata block flattened to their bare names. An indented
// line inside metadata is a metadata key; an indented line elsewhere folds
// into the previous top-level value.
func splitFrontmatter(content string) (meta map[string]string, body string, ok bool) {
	meta = map[string]string{}
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---\n") {
		return nil, content, false
	}
	rest := trimmed[len("---\n"):]
	inMetadata := false
	lastKey := ""
	for {
		line, tail, _ := strings.Cut(rest, "\n")
		rest = tail
		switch trimmedLine := strings.TrimSpace(line); {
		case trimmedLine == "---" || trimmedLine == "...":
			return meta, rest, true
		case trimmedLine == "":
			continue
		case leadingSpaces(line) > 0:
			if inMetadata {
				if key, value, isKey := strings.Cut(trimmedLine, ":"); isKey {
					lastKey = strings.TrimSpace(key)
					meta[lastKey] = strings.TrimSpace(value)
				}
			} else if lastKey != "" {
				meta[lastKey] = strings.TrimSpace(meta[lastKey] + " " + trimmedLine)
			}
		case strings.HasPrefix(line, "metadata:"):
			inMetadata = true
			lastKey = ""
		default:
			inMetadata = false
			if key, value, isKey := strings.Cut(line, ":"); isKey {
				lastKey = strings.TrimSpace(key)
				meta[lastKey] = strings.TrimSpace(value)
			}
		}
		if rest == "" {
			// Unterminated block: the whole content is the body.
			return nil, content, false
		}
	}
}

// leadingSpaces counts a line's leading spaces.
func leadingSpaces(line string) int {
	count := 0
	for count < len(line) && line[count] == ' ' {
		count++
	}
	return count
}

// SlugKey lowercases text and keeps [a-z0-9] runs joined by single dashes,
// capped at max bytes with trailing dashes trimmed. It returns "" when
// nothing usable remains.
func SlugKey(text string, max int) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(text) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if max > 0 && len(slug) > max {
		slug = strings.Trim(slug[:max], "-")
	}
	return slug
}

// NextKey returns base when free, else the first free base-N suffix.
func NextKey(existing map[string]bool, base string) string {
	if !existing[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !existing[candidate] {
			return candidate
		}
	}
}
