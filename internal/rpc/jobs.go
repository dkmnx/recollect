package rpc

import (
	"encoding/json"
	"strings"
	"time"

	"recollect/internal/jobs"
	"recollect/internal/render"
)

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
	section := renderFn(render.Config{
		Enabled: true,
		Store:   s.deps.Store,
		Root:    s.deps.Store.ProjectDir(),
	})
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
	Pending  int `json:"pending"`
}

func (s *server) ingest(req Request) error {
	var p ingestParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.invalid(req.ID, "invalid events.ingest params")
	}
	if err := s.deps.Jobs.Ingest(p.Records, p.Boundaries); err != nil {
		return s.wrap(req.ID, err, jobsFailure)
	}
	pending, err := s.pendingJobs()
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	return s.reply(req.ID, ingestResult{
		Accepted: len(p.Records), Pending: pending,
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
	Action string `json:"action"`
}

type maintenanceResult struct {
	Action  string `json:"action"`
	Pending int    `json:"pending"`
}

func (s *server) maintenanceExecute(req Request) error {
	var p maintenanceParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.invalid(req.ID, "invalid maintenance.execute params")
	}
	if strings.TrimSpace(p.Action) == "" {
		return s.invalid(req.ID, "maintenance action is required")
	}
	if p.Action != "status" {
		return s.invalid(req.ID, "unsupported maintenance action: "+p.Action)
	}
	pending, err := s.pendingJobs()
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	return s.reply(req.ID, maintenanceResult{Action: p.Action, Pending: pending}, nil)
}

func (s *server) jobsComplete(req Request) error {
	var p completeParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return s.invalid(req.ID, "invalid jobs.complete params")
	}
	if strings.TrimSpace(p.JobID) == "" || strings.TrimSpace(p.Lease) == "" {
		return s.invalid(req.ID, "jobID and lease are required")
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

type statusResult struct {
	Health    string `json:"health"`
	Pending   int    `json:"pending"`
	LastError string `json:"lastError,omitempty"`
}

func (s *server) statusGet(req Request) error {
	health := "ok"
	if err := s.deps.Store.Ping(); err != nil {
		health = "storage_failure"
	}
	pending, err := s.pendingJobs()
	if err != nil {
		return s.reply(req.ID, nil, storageFailure(err))
	}
	out := statusResult{Health: health, Pending: pending, LastError: s.lastError}
	return s.reply(req.ID, out, nil)
}

// pendingJobs counts jobs waiting for a lease. A read failure is returned to
// the caller so zero pending work is never reported during an outage.
func (s *server) pendingJobs() (int, error) {
	all, err := s.deps.Jobs.Status()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, job := range all {
		if job.State == jobs.StatePending {
			count++
		}
	}
	return count, nil
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
		if job.State == jobs.StatePending {
			out.Pending = append(out.Pending, viewOf(job))
		}
	}
	out.Count = len(out.Pending)
	s.closed = true
	return s.reply(req.ID, out, nil)
}
