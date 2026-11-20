// Package render is the public facade over Recollect's prompt renderer.
// The implementation lives in recollect/internal/render.
package render

import internalrender "github.com/dkmnx/recollect/internal/render"

// Config holds the host values needed to render one memory section.
type Config = internalrender.Config

// MemorySection returns the bounded memory section for a prompt.
var MemorySection = internalrender.MemorySection
