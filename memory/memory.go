// Package memory is the public facade over Recollect's memory engine. It
// exposes only the types, constants, and constructors that external adapters
// need. The implementation lives in recollect/internal/memory.
package memory

import internalmemory "github.com/dkmnx/recollect/internal/memory"

// Types shared between the engine and its adapters.
type (
	CaptureAttempt  = internalmemory.CaptureAttempt
	CaptureAttempts = internalmemory.CaptureAttempts
	CaptureOp       = internalmemory.CaptureOp
	CapturePhase    = internalmemory.CapturePhase
	CaptureState    = internalmemory.CaptureState
	ChainRevision   = internalmemory.ChainRevision
	Digest          = internalmemory.Digest
	Error           = internalmemory.Error
	Fact            = internalmemory.Fact
	FactMatch       = internalmemory.FactMatch
	IndexInput      = internalmemory.IndexInput
	Limits          = internalmemory.Limits
	Match           = internalmemory.Match
	Options         = internalmemory.Options
	Page            = internalmemory.Page
	ProjectMeta     = internalmemory.ProjectMeta
	Store           = internalmemory.Store

	CaptureResult   = internalmemory.CaptureResult
	DigestRetention = internalmemory.DigestRetention
	JournalEntry    = internalmemory.JournalEntry
	LinksField      = internalmemory.LinksField
	ManifestEntry   = internalmemory.ManifestEntry
	MigrateResult   = internalmemory.MigrateResult
	PageDraft       = internalmemory.PageDraft
	PageDraftResult = internalmemory.PageDraftResult
	PageSpec        = internalmemory.PageSpec

	ScoredFactForTest = internalmemory.ScoredFactForTest
)

// Constants used by tool contracts, capture, and prompt rendering.
const (
	CaptureConsolidationPrompt = internalmemory.CaptureConsolidationPrompt
	CaptureDigestPrompt        = internalmemory.CaptureDigestPrompt
	CaptureOverflowTopic       = internalmemory.CaptureOverflowTopic
	CapturePhaseDigest         = internalmemory.CapturePhaseDigest
	CapturePhaseTyped          = internalmemory.CapturePhaseTyped
	CodeCaptureTimeout         = internalmemory.CodeCaptureTimeout
	CodeDigestNotFound         = internalmemory.CodeDigestNotFound
	CodeDisabled               = internalmemory.CodeDisabled
	CodeFactNotFound           = internalmemory.CodeFactNotFound
	CodeInvalidOperation       = internalmemory.CodeInvalidOperation
	CodeIO                     = internalmemory.CodeIO
	CodeMissingField           = internalmemory.CodeMissingField
	CodeOldAmbiguous           = internalmemory.CodeOldAmbiguous
	CodeOldNotFound            = internalmemory.CodeOldNotFound
	CodeReasonRequired         = internalmemory.CodeReasonRequired
	CodeSecretRejected         = internalmemory.CodeSecretRejected
	DefaultDigestMaxBytes      = internalmemory.DefaultDigestMaxBytes
	DefaultDigestTTLDays       = internalmemory.DefaultDigestTTLDays
	DefaultMaxDigests          = internalmemory.DefaultMaxDigests
	DefaultPagesInjectBytes    = internalmemory.DefaultPagesInjectBytes
	EvolvePagesPrompt          = internalmemory.EvolvePagesPrompt
	EvolvePrompt               = internalmemory.EvolvePrompt
	FactSceneChat              = internalmemory.FactSceneChat
	FactSceneCoding            = internalmemory.FactSceneCoding
	FactSceneWorking           = internalmemory.FactSceneWorking
	FactTypeExperience         = internalmemory.FactTypeExperience
	FactTypeProject            = internalmemory.FactTypeProject
	IndexLineLimit             = internalmemory.IndexLineLimit
	MemoryInstructions         = internalmemory.MemoryInstructions
	RedactedPlaceholder        = internalmemory.RedactedPlaceholder

	AgentMemoryScopeLine  = internalmemory.AgentMemoryScopeLine
	FactNodeType          = internalmemory.FactNodeType
	MinDigestSummaryBytes = internalmemory.MinDigestSummaryBytes

	ArchiveDigestKeepForTest  = internalmemory.ArchiveDigestKeepForTest
	JournalPrevFileForTest    = internalmemory.JournalPrevFileForTest
	JournalRotateBytesForTest = internalmemory.JournalRotateBytesForTest
	MaxFactLinksForTest       = internalmemory.MaxFactLinksForTest
	MaxFactNameBytesForTest   = internalmemory.MaxFactNameBytesForTest
)

// Functions forwarded to the implementation. Each is a variable alias so the
// facade adds no second implementation of any rule.
var (
	AgentMemoriesRoot        = internalmemory.AgentMemoriesRoot
	AgentMemoryPromptSection = internalmemory.AgentMemoryPromptSection
	AgentMemoryRoot          = internalmemory.AgentMemoryRoot
	ApplyCaptureOp           = internalmemory.ApplyCaptureOp
	ArchiveDir               = internalmemory.ArchiveDir
	ArchiveSessionsDir       = internalmemory.ArchiveSessionsDir
	BuildIndex               = internalmemory.BuildIndex
	ChainBackfillCandidates  = internalmemory.ChainBackfillCandidates
	CodeOf                   = internalmemory.CodeOf
	CollapseLine             = internalmemory.CollapseLine
	CountFacts               = internalmemory.CountFacts
	DetectProjectRoot        = internalmemory.DetectProjectRoot
	DigestLine               = internalmemory.DigestLine
	DigestUsable             = internalmemory.DigestUsable
	Errorf                   = internalmemory.Errorf
	FactSlug                 = internalmemory.FactSlug
	FilterEvolveOps          = internalmemory.FilterEvolveOps
	FilterPageDrafts         = internalmemory.FilterPageDrafts
	FormatDigest             = internalmemory.FormatDigest
	FormatFact               = internalmemory.FormatFact
	FormatPage               = internalmemory.FormatPage
	HeadBytes                = internalmemory.HeadBytes
	IndexFile                = internalmemory.IndexFile
	JournalFile              = internalmemory.JournalFile
	MemoryRoot               = internalmemory.MemoryRoot
	NewStore                 = internalmemory.NewStore
	NextKey                  = internalmemory.NextKey
	NormalizeFactType        = internalmemory.NormalizeFactType
	NormalizeNewlines        = internalmemory.NormalizeNewlines
	NormalizeScene           = internalmemory.NormalizeScene
	PageCatalog              = internalmemory.PageCatalog
	PageQuestion             = internalmemory.PageQuestion
	PagesDir                 = internalmemory.PagesDir
	ParseCaptureDigest       = internalmemory.ParseCaptureDigest
	ParseCaptureResult       = internalmemory.ParseCaptureResult
	ParsePageDrafts          = internalmemory.ParsePageDrafts
	ProjectDir               = internalmemory.ProjectDir
	ProjectDirForKey         = internalmemory.ProjectDirForKey
	ProjectKey               = internalmemory.ProjectKey
	ProjectMetaFor           = internalmemory.ProjectMetaFor
	Redact                   = internalmemory.Redact
	SanitizeAgentMemoryKey   = internalmemory.SanitizeAgentMemoryKey
	SecretHoldback           = internalmemory.SecretHoldback
	SecretLike               = internalmemory.SecretLike
	SecretPatterns           = internalmemory.SecretPatterns
	SessionsDir              = internalmemory.SessionsDir
	TailBytes                = internalmemory.TailBytes
	TopicsDir                = internalmemory.TopicsDir
	UserFile                 = internalmemory.UserFile

	ApplyEdit           = internalmemory.ApplyEdit
	CaptureAttemptFile  = internalmemory.CaptureAttemptFile
	CaptureStateFile    = internalmemory.CaptureStateFile
	ChainFile           = internalmemory.ChainFile
	DefaultLimits       = internalmemory.DefaultLimits
	IsFullyPrivate      = internalmemory.IsFullyPrivate
	ParseDigest         = internalmemory.ParseDigest
	ParseFact           = internalmemory.ParseFact
	ParsePage           = internalmemory.ParsePage
	ProjectMetaFile     = internalmemory.ProjectMetaFile
	RenderIndex         = internalmemory.RenderIndex
	RenderManifest      = internalmemory.RenderManifest
	RetainDigests       = internalmemory.RetainDigests
	SearchLines         = internalmemory.SearchLines
	SlugKey             = internalmemory.SlugKey
	StripPrivateContent = internalmemory.StripPrivateContent
	TruncateIndex       = internalmemory.TruncateIndex
	ValidTopicName      = internalmemory.ValidTopicName

	RankFactsForTest         = internalmemory.RankFactsForTest
	PrepareFactForTest       = internalmemory.PrepareFactForTest
	ManifestKindRankForTest  = internalmemory.ManifestKindRankForTest
	ContainPathForTest       = internalmemory.ContainPathForTest
	DecodeJournalLineForTest = internalmemory.DecodeJournalLineForTest
	ExtractJSONObjectForTest = internalmemory.ExtractJSONObjectForTest
	RepairJSONEscapesForTest = internalmemory.RepairJSONEscapesForTest
	SlugifyForTest           = internalmemory.SlugifyForTest
)
