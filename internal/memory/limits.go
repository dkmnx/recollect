package memory

// Limits bounds the read-side and housekeeping surfaces: search result
// caps, prompt-injection clamps, digest retention, and the capture
// manifest budgets. Write paths carry no size caps (ADR-005): the only
// size guard in the system is prompt-injection truncation. One copy lives
// here so readers cannot drift.
type Limits struct {
	// ProjectSoftBytes is advisory: it drives the capture "index is nearly
	// full" hint and nothing else.
	ProjectSoftBytes int
	// UserInjectBytes clamps how much of user.md is injected into the
	// prompt; the file itself is uncapped.
	UserInjectBytes int
	// PagesInjectBytes clamps the derived knowledge pages injected into the
	// prompt; the page files are write-clamped smaller (page.go).
	PagesInjectBytes int
	MaxSearchResults int
	// MaxDigests caps the searchable digest set: 0 uses the default, and a
	// negative value keeps every digest.
	MaxDigests int
	// DigestTTLDays ages digests out by last capture: 0 uses the default,
	// and a negative value disables the age bound.
	DigestTTLDays        int
	ManifestMaxFiles     int
	ManifestPreviewLines int
	ManifestMaxBytes     int
}

const (
	DefaultProjectSoftBytes     = 16 * 1024
	DefaultUserInjectBytes      = 4 * 1024
	DefaultPagesInjectBytes     = 10 * 1024
	DefaultMaxSearchResults     = 50
	DefaultMaxDigests           = 20
	DefaultDigestTTLDays        = 30
	DefaultManifestMaxFiles     = 200
	DefaultManifestPreviewLines = 30
	DefaultManifestMaxBytes     = 6 * 1024
)

// DefaultLimits returns the shipped bounds.
func DefaultLimits() Limits {
	return Limits{
		ProjectSoftBytes:     DefaultProjectSoftBytes,
		UserInjectBytes:      DefaultUserInjectBytes,
		PagesInjectBytes:     DefaultPagesInjectBytes,
		MaxSearchResults:     DefaultMaxSearchResults,
		MaxDigests:           DefaultMaxDigests,
		DigestTTLDays:        DefaultDigestTTLDays,
		ManifestMaxFiles:     DefaultManifestMaxFiles,
		ManifestPreviewLines: DefaultManifestPreviewLines,
		ManifestMaxBytes:     DefaultManifestMaxBytes,
	}
}

// withDefaults fills zero fields from DefaultLimits so callers can override
// only what a test cares about.
func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.ProjectSoftBytes <= 0 {
		l.ProjectSoftBytes = d.ProjectSoftBytes
	}
	if l.UserInjectBytes <= 0 {
		l.UserInjectBytes = d.UserInjectBytes
	}
	if l.PagesInjectBytes <= 0 {
		l.PagesInjectBytes = d.PagesInjectBytes
	}
	if l.MaxSearchResults <= 0 {
		l.MaxSearchResults = d.MaxSearchResults
	}
	if l.MaxDigests == 0 {
		l.MaxDigests = d.MaxDigests
	}
	if l.DigestTTLDays == 0 {
		l.DigestTTLDays = d.DigestTTLDays
	}
	if l.ManifestMaxFiles <= 0 {
		l.ManifestMaxFiles = d.ManifestMaxFiles
	}
	if l.ManifestPreviewLines <= 0 {
		l.ManifestPreviewLines = d.ManifestPreviewLines
	}
	if l.ManifestMaxBytes <= 0 {
		l.ManifestMaxBytes = d.ManifestMaxBytes
	}
	return l
}
