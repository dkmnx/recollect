package rpc

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/dkmnx/recollect/internal/imports"
	"github.com/dkmnx/recollect/internal/jobs"
	"github.com/dkmnx/recollect/internal/render"
)

var knownFailureCodes = map[string]bool{
	"cancelled":              true,
	"generation_unavailable": true,
	"generation_timeout":     true,
	"generation_failed":      true,
}

type contextParams struct {
	Refresh bool `json:"refresh"`
}

type contextResult struct {
	SnapshotID string `json:"snapshotID"`
	Section    string `json:"section"`
}

func (s *server) contextGet(req Request) error {
	var p contextParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.invalid(req.ID, "invalid context.get params")
	}
	if !p.Refresh && s.snapshotID != "" {
		return s.reply(req.ID, contextResult{
			SnapshotID: s.snapshotID, Section: s.snapshotText,
		}, nil)
	}
	renderFn := s.deps.Render
	if renderFn == nil {
		renderFn = render.MemorySection
	}
	section := filterModelText(renderFn(render.Config{
		Enabled: true,
		Store:   s.deps.Store,
		Root:    projectRootFor(s.bindings),
	}))
	s.snapshotText = section
	s.snapshotID = snapshotHash(section + "\x00" + s.bindings.ProjectDir + "\x00" + s.bindings.HarnessID)
	return s.reply(req.ID, contextResult{
		SnapshotID: s.snapshotID, Section: s.snapshotText,
	}, nil)
}

type ingestParams struct {
	Records    []jobs.HostRecord  `json:"records"`
	Boundaries []jobs.RunBoundary `json:"boundaries"`
}

type ingestResult struct {
	Accepted int `json:"accepted"`
	Dropped  int `json:"dropped"`
	Pending  int `json:"pending"`
}

func (s *server) ingest(req Request) error {
	var p ingestParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.invalid(req.ID, "invalid events.ingest params")
	}
	accepted, dropped, err := s.deps.Jobs.Ingest(p.Records, p.Boundaries)
	if err != nil {
		return s.wrap(req.ID, err, jobsFailure)
	}
	pending, _, err := s.jobCounts()
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	return s.reply(req.ID, ingestResult{
		Accepted: accepted, Dropped: dropped, Pending: pending,
	}, nil)
}

type nextResult struct {
	Job   *jobView `json:"job"`
	Lease string   `json:"lease"`
}

type jobView struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	HarnessID     string `json:"harnessID"`
	State         string `json:"state"`
	Prompt        string `json:"prompt,omitempty"`
	Deterministic bool   `json:"deterministic"`
}

func viewOf(job jobs.Job) jobView {
	return jobView{
		ID: job.ID, Kind: string(job.Kind),
		HarnessID: job.HarnessID, State: string(job.State),
		Prompt: job.Prompt, Deterministic: job.Deterministic,
	}
}

type nextParams struct {
	Generation *bool `json:"generation"`
}

func (s *server) jobsNext(req Request) error {
	var p nextParams
	if len(req.Params) > 0 && string(req.Params) != "null" {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return s.invalid(req.ID, "invalid jobs.next params")
		}
	}
	generation := true
	if p.Generation != nil {
		generation = *p.Generation
	}
	lease, err := newLease()
	if err != nil {
		return s.reply(req.ID, nil, failure(CodeStorage, "lease_unavailable"))
	}
	job, err := s.deps.Jobs.NextCapable(s.bindings.HarnessID, lease, generation)
	if err != nil {
		return s.wrap(req.ID, err, jobsFailure)
	}
	out := nextResult{}
	if job != nil {
		view := viewOf(*job)
		out.Job = &view
		out.Lease = lease
	}
	return s.reply(req.ID, out, nil)
}

type completeParams struct {
	JobID   string `json:"jobID"`
	Lease   string `json:"lease"`
	Output  string `json:"output"`
	Failure string `json:"failure"`
}

type maintenanceParams struct {
	Action           string `json:"action"`
	SourceProjectDir string `json:"sourceProjectDir"`
	SourceUserDir    string `json:"sourceUserDir"`
	DryRun           bool   `json:"dryRun"`
}

type maintenanceResult struct {
	Action  string `json:"action"`
	Pending int    `json:"pending"`
}

type importStatusResult struct {
	Report imports.Report `json:"report"`
	Exists bool           `json:"exists"`
}

func (s *server) maintenanceExecute(req Request) error {
	var p maintenanceParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.invalid(req.ID, "invalid maintenance.execute params")
	}
	if strings.TrimSpace(p.Action) == "" {
		return s.invalid(req.ID, "maintenance action is required")
	}
	switch p.Action {
	case "status":
		return s.maintenanceStatus(req, p.Action)
	case "refresh":
		if err := s.deps.Store.RefreshSearch(); err != nil {
			return s.reply(req.ID, nil, storageFailure(err))
		}
		return s.reply(req.ID, map[string]any{"action": "refresh"}, nil)
	case "import":
		return s.maintenanceImport(req, p)
	case "import-status":
		return s.maintenanceImportStatus(req)
	default:
		return s.invalid(req.ID, "unsupported maintenance action: "+p.Action)
	}
}

func (s *server) maintenanceStatus(req Request, action string) error {
	pending, _, err := s.jobCounts()
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	return s.reply(req.ID, maintenanceResult{Action: action, Pending: pending}, nil)
}

func (s *server) maintenanceImport(req Request, p maintenanceParams) error {
	if strings.TrimSpace(p.SourceProjectDir) == "" || strings.TrimSpace(p.SourceUserDir) == "" {
		return s.invalid(req.ID, "sourceProjectDir and sourceUserDir are required")
	}
	report, err := imports.Run(imports.Options{
		Source:        imports.Source{ProjectDir: p.SourceProjectDir, UserDir: p.SourceUserDir},
		Target:        s.deps.Store,
		TargetDir:     s.bindings.ProjectDir,
		TargetUserDir: s.bindings.UserDir,
		DryRun:        p.DryRun,
	})
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	return s.reply(req.ID, report, nil)
}

func (s *server) maintenanceImportStatus(req Request) error {
	report, exists, err := imports.Status(s.bindings.ProjectDir)
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	return s.reply(req.ID, importStatusResult{Report: report, Exists: exists}, nil)
}

func (s *server) jobsComplete(req Request) error {
	var p completeParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.invalid(req.ID, "invalid jobs.complete params")
	}
	if strings.TrimSpace(p.JobID) == "" || strings.TrimSpace(p.Lease) == "" {
		return s.invalid(req.ID, "jobID and lease are required")
	}
	if p.Failure != "" && !knownFailureCodes[p.Failure] {
		return s.invalid(req.ID, "unsupported failure code")
	}
	var err error
	if p.Failure != "" {
		err = s.deps.Jobs.Fail(p.JobID, p.Lease, p.Failure)
	} else {
		err = s.deps.Jobs.Complete(p.JobID, p.Lease, p.Output)
	}
	if err != nil {
		return s.wrap(req.ID, err, jobsFailure)
	}
	return s.reply(req.ID, map[string]any{"id": p.JobID, "applied": true}, nil)
}

type statusLimits struct {
	ProjectSoftBytes int `json:"projectSoftBytes"`
	UserInjectBytes  int `json:"userInjectBytes"`
	PagesInjectBytes int `json:"pagesInjectBytes"`
	MaxSearchResults int `json:"maxSearchResults"`
	MaxDigests       int `json:"maxDigests"`
	DigestTTLDays    int `json:"digestTTLDays"`
}

type statusResult struct {
	Health           string         `json:"health"`
	Pending          int            `json:"pending"`
	Conflicts        int            `json:"conflicts"`
	GenerationState  string         `json:"generationState"`
	JobStates        map[string]int `json:"jobStates"`
	PrivacyBlocked   int            `json:"privacyBlocked"`
	Facts            int            `json:"facts"`
	UserBytes        int            `json:"userBytes"`
	Digests          int            `json:"digests"`
	PagesNeedRefresh []string       `json:"pagesNeedRefresh"`
	Limits           statusLimits   `json:"limits"`
	LastErrorCode    string         `json:"lastErrorCode,omitempty"`
}

func (s *server) statusGet(req Request) error {
	health := "ok"
	facts, err := s.deps.Store.ListFacts()
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	user, err := s.deps.Store.ReadUser()
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	digests, err := s.deps.Store.ListDigests()
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	stalePages, err := s.deps.Store.StalePages()
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	all, err := s.deps.Jobs.Status()
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	states := map[string]int{
		string(jobs.StatePending): 0, string(jobs.StateLeased): 0,
		string(jobs.StateApplied): 0, string(jobs.StateRetryWait): 0,
		string(jobs.StateConflict): 0, string(jobs.StateCancelled): 0,
	}
	privacyBlocked := 0
	for _, job := range all {
		states[string(job.State)]++
		if job.State == jobs.StateConflict && job.Error == jobs.PrivacyPolicyRequired {
			privacyBlocked++
		}
	}
	limits := s.deps.Store.Limits()
	out := statusResult{
		Health:          health,
		Pending:         states[string(jobs.StatePending)] + states[string(jobs.StateRetryWait)],
		Conflicts:       states[string(jobs.StateConflict)],
		GenerationState: s.generationState,
		JobStates:       states, PrivacyBlocked: privacyBlocked,
		Facts: len(facts), UserBytes: len(user), Digests: len(digests),
		PagesNeedRefresh: stalePages,
		Limits: statusLimits{
			ProjectSoftBytes: limits.ProjectSoftBytes, UserInjectBytes: limits.UserInjectBytes,
			PagesInjectBytes: limits.PagesInjectBytes, MaxSearchResults: limits.MaxSearchResults,
			MaxDigests: limits.MaxDigests, DigestTTLDays: limits.DigestTTLDays,
		},
		LastErrorCode: s.lastErrorCode,
	}
	return s.reply(req.ID, out, nil)
}

// jobCounts reports pending work and parked conflicts. A read failure is
// returned to the caller so zero pending work is never reported during an outage.
func (s *server) jobCounts() (pending, conflicts int, err error) {
	all, err := s.deps.Jobs.Status()
	if err != nil {
		return 0, 0, err
	}
	for _, job := range all {
		switch job.State {
		case jobs.StatePending, jobs.StateRetryWait:
			pending++
		case jobs.StateConflict:
			conflicts++
		}
	}
	return pending, conflicts, nil
}

type closeParams struct {
	DeadlineMS int `json:"deadlineMS"`
}

type closeResult struct {
	Count    int       `json:"count"`
	Deadline int       `json:"deadlineMS"`
	Pending  []jobView `json:"pending"`
}

func (s *server) engineClose(req Request) error {
	var p closeParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.invalid(req.ID, "invalid engine.close params")
	}
	if p.DeadlineMS <= 0 {
		p.DeadlineMS = 5000
	}
	deadline := time.Now().Add(time.Duration(p.DeadlineMS) * time.Millisecond)
	if err := s.deps.Jobs.Recover(); err != nil {
		return s.wrap(req.ID, err, jobsFailure)
	}
	if time.Now().After(deadline) {
		return s.reply(req.ID, nil, failure(CodeBusy, "close deadline exceeded"))
	}
	all, err := s.deps.Jobs.Status()
	if err != nil {
		return s.wrap(req.ID, err, jobsFailure)
	}
	out := closeResult{Count: 0, Deadline: p.DeadlineMS, Pending: []jobView{}}
	for _, job := range all {
		if job.State == jobs.StatePending || job.State == jobs.StateRetryWait {
			out.Pending = append(out.Pending, viewOf(job))
		}
	}
	out.Count = len(out.Pending)
	s.closed = true
	return s.reply(req.ID, out, nil)
}
