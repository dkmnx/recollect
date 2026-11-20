// Package imports is the public facade over Recollect's copy-first importer.
// The implementation lives in recollect/internal/imports.
package imports

import internalimports "github.com/dkmnx/recollect/internal/imports"

// Types shared between a host adapter and the importer.
type (
	Mapping = internalimports.Mapping
	Options = internalimports.Options
	Report  = internalimports.Report
	Source  = internalimports.Source
)

// Run copies supported source state into a target. Status reads the last manifest.
var (
	Run    = internalimports.Run
	Status = internalimports.Status
)
