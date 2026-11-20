// Package render builds the memory section of a system prompt.
// The adapter supplies the enablement flag, store, and project root.
package render

import (
	"fmt"
	"strings"

	"github.com/dkmnx/recollect/internal/memory"
)

// Config holds the host values needed to render one prompt section.
// Store must be non-nil when Enabled is true.
type Config struct {
	Enabled bool
	Store   *memory.Store
	Root    string
}

// MemorySection returns an empty string when memory is disabled.
// The adapter composes the result into its own system prompt.
func MemorySection(cfg Config) string {
	if !cfg.Enabled {
		return ""
	}
	limits := cfg.Store.Limits()

	blocks := []string{"## Memory", memory.MemoryInstructions}
	if user, err := cfg.Store.ReadUser(); err == nil {
		if block := userMemoryBlock(cfg.Store, user, limits); block != "" {
			blocks = append(blocks, block)
		}
	}
	if block := projectIndexBlock(cfg.Store, cfg.Root, limits); block != "" {
		blocks = append(blocks, block)
	}
	if block := pagesBlock(cfg.Store, limits); block != "" {
		blocks = append(blocks, block)
	}
	return "\n\n" + strings.Join(blocks, "\n\n")
}

func userMemoryBlock(store *memory.Store, user string, limits memory.Limits) string {
	if strings.TrimSpace(user) == "" {
		return ""
	}
	filtered := memory.Redact(memory.StripPrivateContent(user))
	body, truncated := memory.TailBytes(filtered, limits.UserInjectBytes)
	if truncated {
		body += "\n\n[truncated; full file: " + store.UserPath() + "]"
	}
	return `<user_memory path="` + escapeXML(store.UserPath()) + `">` + "\n" + body + "\n</user_memory>"
}

func projectIndexBlock(store *memory.Store, root string, limits memory.Limits) string {
	index, err := store.ReadIndex()
	if err != nil {
		return ""
	}
	digests, _ := store.ListDigests()
	if strings.TrimSpace(index) == "" && len(digests) == 0 {
		return ""
	}
	meta := memory.ProjectMetaFor(root)
	return memory.BuildIndex(memory.IndexInput{
		Name:      meta.Name,
		Root:      meta.Root,
		IndexPath: store.IndexPath(),
		Index:     index,
		Digests:   digests,
	})
}

func pagesBlock(store *memory.Store, limits memory.Limits) string {
	pages, err := store.ListPages()
	if err != nil || len(pages) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<project_pages>")
	for _, page := range pages {
		fmt.Fprintf(&b, "\n<page name=%q question=%q>\n%s\n</page>",
			escapeXML(page.Name), escapeXML(page.Question), page.Body)
	}
	b.WriteString("\n</project_pages>")
	block := b.String()
	if len(block) <= limits.PagesInjectBytes {
		return block
	}
	clamped, _ := memory.HeadBytes(block, limits.PagesInjectBytes)
	return clamped + "\n[truncated; full pages: " + memory.PagesDir(store.ProjectDir()) + "]"
}
