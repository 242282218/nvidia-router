package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/xkproxy"
)

const (
	modelTestJobTTL        = 30 * time.Minute
	modelTestMaxModels     = 500
	modelTestDefaultLimit  = 4
	modelTestMinLimit      = 2
	modelTestMaxLimit      = 8
	modelTestMaxActiveJobs = 4
)

type modelTestRunner interface {
	List(context.Context) ([]modelcatalog.Model, error)
	TestModel(context.Context, string, int64, int64) error
}

type modelTestJobRequest struct {
	Provider     string  `json:"provider"`
	CredentialID *int64  `json:"credential_id,omitempty"`
	ModelIDs     []int64 `json:"model_ids"`
	Mode         string  `json:"mode"`
	Concurrency  int     `json:"concurrency"`
}

type modelTestJobResult struct {
	ModelID    int64      `json:"model_id"`
	PublicID   string     `json:"public_id"`
	Status     string     `json:"status"`
	Duration   *int64     `json:"duration_ms,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type modelTestJob struct {
	ID              string               `json:"id"`
	Provider        string               `json:"provider"`
	CredentialID    int64                `json:"-"`
	Mode            string               `json:"mode"`
	Concurrency     int                  `json:"concurrency"`
	Status          string               `json:"status"`
	CreatedAt       time.Time            `json:"created_at"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	FinishedAt      *time.Time           `json:"finished_at,omitempty"`
	Total           int                  `json:"total"`
	Completed       int                  `json:"completed"`
	Results         []modelTestJobResult `json:"results"`
	Cancel          context.CancelFunc   `json:"-"`
	Context         context.Context      `json:"-"`
	CancelRequested bool                 `json:"-"`
}

type ModelTestJobs struct {
	runner modelTestRunner
	mu     sync.Mutex
	jobs   map[string]*modelTestJob
	seq    atomic.Uint64
	now    func() time.Time
}

func NewModelTestJobs(runner modelTestRunner) *ModelTestJobs {
	return &ModelTestJobs{runner: runner, jobs: make(map[string]*modelTestJob), now: time.Now}
}

func (h *ModelTestJobs) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/admin/api/model-test-jobs" && request.Method == http.MethodPost:
		h.create(writer, request)
	case strings.HasPrefix(request.URL.Path, "/admin/api/model-test-jobs/") && request.Method == http.MethodGet:
		h.get(writer, request)
	case strings.HasPrefix(request.URL.Path, "/admin/api/model-test-jobs/") && request.Method == http.MethodDelete:
		h.cancel(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (h *ModelTestJobs) create(writer http.ResponseWriter, request *http.Request) {
	var input modelTestJobRequest
	if err := decodeJSON(writer, request, &input); err != nil {
		writeInvalidRequest(writer, "The model test job request is invalid.", err)
		return
	}
	provider, concurrency, err := validateModelTestJobRequest(input)
	if err != nil {
		writeInvalidRequest(writer, "The model test job request is invalid.", err)
		return
	}
	if provider == modelcatalog.ProviderOpenCodeFree {
		if configured, ok := h.runner.(interface{ OpenCodeFreeConfigured() bool }); ok && !configured.OpenCodeFreeConfigured() {
			writeAdminError(writer, http.StatusServiceUnavailable, "provider_not_configured", "The selected model provider is not configured.", nil)
			return
		}
	}
	if provider == modelcatalog.ProviderNVIDIA && input.CredentialID != nil {
		if validator, ok := h.runner.(interface {
			ValidateNVIDIAKey(context.Context, int64) error
		}); ok {
			if err := validator.ValidateNVIDIAKey(request.Context(), *input.CredentialID); err != nil {
				writeAdminError(writer, http.StatusBadRequest, "invalid_credential", "The selected NVIDIA Key is not available.", nil)
				return
			}
		}
	}
	models, err := h.runner.List(request.Context())
	if err != nil {
		writeInternalError(writer, err)
		return
	}
	refs, err := selectTestModels(models, provider, input.ModelIDs)
	if err != nil {
		writeInvalidRequest(writer, "The selected models are invalid for this test channel.", err)
		return
	}
	job, err := h.newJob(provider, input, concurrency, refs)
	if err != nil {
		writeAdminError(writer, http.StatusTooManyRequests, "model_test_capacity", "Too many model test jobs are already running.", nil)
		return
	}
	writeJSON(writer, http.StatusAccepted, h.snapshot(job))
	go h.run(job)
}

func (h *ModelTestJobs) get(writer http.ResponseWriter, request *http.Request) {
	id, ok := modelTestJobID(request.URL.Path)
	if !ok {
		http.NotFound(writer, request)
		return
	}
	job, ok := h.find(id)
	if !ok {
		http.NotFound(writer, request)
		return
	}
	writeJSON(writer, http.StatusOK, h.snapshot(job))
}

func (h *ModelTestJobs) cancel(writer http.ResponseWriter, request *http.Request) {
	id, ok := modelTestJobID(request.URL.Path)
	if !ok {
		http.NotFound(writer, request)
		return
	}
	h.mu.Lock()
	h.pruneLocked()
	job, exists := h.jobs[id]
	if !exists {
		h.mu.Unlock()
		http.NotFound(writer, request)
		return
	}
	if job.Status == "queued" || job.Status == "running" {
		job.CancelRequested = true
		job.Cancel()
		finished := h.now().UTC()
		job.Status = "cancelled"
		job.FinishedAt = &finished
		for index := range job.Results {
			if isModelTestResultTerminal(job.Results[index].Status) {
				continue
			}
			job.Results[index].Status = "cancelled"
			job.Results[index].Error = "任务已取消"
			job.Results[index].FinishedAt = &finished
		}
		job.Completed = job.Total
	}
	snapshot := cloneModelTestJob(job)
	h.mu.Unlock()
	writeJSON(writer, http.StatusOK, snapshot)
}

type modelRef struct {
	ID       int64
	PublicID string
}

func (h *ModelTestJobs) newJob(provider string, input modelTestJobRequest, concurrency int, refs []modelRef) (*modelTestJob, error) {
	ctx, cancel := context.WithCancel(context.Background())
	id := strconv.FormatUint(h.seq.Add(1), 10)
	created := h.now().UTC()
	results := make([]modelTestJobResult, 0, len(refs))
	for _, ref := range refs {
		results = append(results, modelTestJobResult{ModelID: ref.ID, PublicID: ref.PublicID, Status: "queued"})
	}
	job := &modelTestJob{
		ID: id, Provider: provider, CredentialID: dereferenceCredential(input.CredentialID),
		Mode: input.Mode, Concurrency: concurrency, Status: "queued", CreatedAt: created,
		Total: len(refs), Results: results, Cancel: cancel, Context: ctx,
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneLocked()
	if h.activeJobsLocked() >= modelTestMaxActiveJobs {
		return nil, errors.New("model test job capacity reached")
	}
	h.jobs[id] = job
	return job, nil
}

func (h *ModelTestJobs) run(job *modelTestJob) {
	h.markRunning(job)
	indices := make(chan int)
	var workers sync.WaitGroup
	for index := 0; index < job.Concurrency; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for resultIndex := range indices {
				h.runOne(job, resultIndex)
			}
		}()
	}
	for index := range job.Results {
		select {
		case indices <- index:
		case <-job.Context.Done():
			break
		}
		if job.Context.Err() != nil {
			break
		}
	}
	close(indices)
	workers.Wait()
	h.finish(job)
}

func (h *ModelTestJobs) markRunning(job *modelTestJob) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if job.CancelRequested {
		return
	}
	started := h.now().UTC()
	job.Status = "running"
	job.StartedAt = &started
}

func (h *ModelTestJobs) runOne(job *modelTestJob, resultIndex int) {
	h.mu.Lock()
	if job.CancelRequested || job.Results[resultIndex].Status != "queued" {
		h.mu.Unlock()
		return
	}
	started := h.now().UTC()
	job.Results[resultIndex].Status = "running"
	job.Results[resultIndex].StartedAt = &started
	h.mu.Unlock()

	startedClock := time.Now()
	err := h.runner.TestModel(job.Context, job.Provider, job.CredentialID, job.Results[resultIndex].ModelID)
	duration := time.Since(startedClock).Milliseconds()
	finished := h.now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	if job.CancelRequested {
		return
	}
	result := &job.Results[resultIndex]
	result.Duration = &duration
	result.FinishedAt = &finished
	if err != nil {
		result.Status = "failed"
		result.Error = safeModelTestError(err)
	} else {
		result.Status = "success"
	}
	job.Completed++
}

func (h *ModelTestJobs) finish(job *modelTestJob) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if job.CancelRequested {
		return
	}
	finished := h.now().UTC()
	job.Completed = 0
	hadFailure := false
	for index := range job.Results {
		if job.Results[index].Status == "failed" {
			hadFailure = true
		}
		if isModelTestResultTerminal(job.Results[index].Status) {
			job.Completed++
		}
	}
	job.Status = "completed"
	if hadFailure {
		job.Status = "failed"
	}
	job.FinishedAt = &finished
}

func (h *ModelTestJobs) find(id string) (*modelTestJob, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneLocked()
	job, ok := h.jobs[id]
	return job, ok
}

func (h *ModelTestJobs) pruneLocked() {
	cutoff := h.now().Add(-modelTestJobTTL)
	for id, job := range h.jobs {
		if job.Status == "queued" || job.Status == "running" {
			continue
		}
		last := job.CreatedAt
		if job.FinishedAt != nil {
			last = *job.FinishedAt
		}
		if last.Before(cutoff) {
			delete(h.jobs, id)
		}
	}
}

func (h *ModelTestJobs) activeJobsLocked() int {
	active := 0
	for _, job := range h.jobs {
		if job.Status == "queued" || job.Status == "running" {
			active++
		}
	}
	return active
}

func (h *ModelTestJobs) snapshot(job *modelTestJob) modelTestJob {
	h.mu.Lock()
	defer h.mu.Unlock()
	return cloneModelTestJob(job)
}

func cloneModelTestJob(job *modelTestJob) modelTestJob {
	copyJob := *job
	copyJob.Results = append([]modelTestJobResult(nil), job.Results...)
	copyJob.Cancel = nil
	copyJob.Context = nil
	return copyJob
}

func validateModelTestJobRequest(input modelTestJobRequest) (string, int, error) {
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if provider != modelcatalog.ProviderNVIDIA && provider != modelcatalog.ProviderOpenCodeFree {
		return "", 0, fmt.Errorf("unsupported test provider %q", input.Provider)
	}
	if len(input.ModelIDs) == 0 || len(input.ModelIDs) > modelTestMaxModels {
		return "", 0, errors.New("model_ids must contain between 1 and 500 models")
	}
	seen := make(map[int64]struct{}, len(input.ModelIDs))
	for _, id := range input.ModelIDs {
		if id <= 0 {
			return "", 0, errors.New("model_ids must contain positive integers")
		}
		if _, exists := seen[id]; exists {
			return "", 0, errors.New("model_ids must not contain duplicates")
		}
		seen[id] = struct{}{}
	}
	if input.Mode != "sequential" && input.Mode != "concurrent" {
		return "", 0, errors.New("mode must be sequential or concurrent")
	}
	if provider == modelcatalog.ProviderNVIDIA && (input.CredentialID == nil || *input.CredentialID <= 0) {
		return "", 0, modelcatalog.ErrNVIDIAKeyRequired
	}
	if provider == modelcatalog.ProviderOpenCodeFree && input.CredentialID != nil && *input.CredentialID != 0 {
		return "", 0, errors.New("OpenCodeFree does not accept a credential_id")
	}
	if input.Mode == "sequential" {
		return provider, 1, nil
	}
	concurrency := input.Concurrency
	if concurrency == 0 {
		concurrency = modelTestDefaultLimit
	}
	if concurrency < modelTestMinLimit || concurrency > modelTestMaxLimit {
		return "", 0, errors.New("concurrency must be between 2 and 8")
	}
	return provider, concurrency, nil
}

func selectTestModels(models []modelcatalog.Model, provider string, ids []int64) ([]modelRef, error) {
	byID := make(map[int64]modelRef, len(models))
	for _, model := range models {
		modelProvider := model.Provider
		if modelProvider == "" {
			modelProvider = modelcatalog.ProviderNVIDIA
		}
		byID[model.ID] = modelRef{ID: model.ID, PublicID: model.PublicID}
		if modelProvider != provider {
			continue
		}
		byID[model.ID] = modelRef{ID: model.ID, PublicID: model.PublicID}
	}
	refs := make([]modelRef, 0, len(ids))
	for _, id := range ids {
		model, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf("model %d was not found", id)
		}
		for _, candidate := range models {
			if candidate.ID == id {
				providerValue := candidate.Provider
				if providerValue == "" {
					providerValue = modelcatalog.ProviderNVIDIA
				}
				if providerValue != provider {
					return nil, fmt.Errorf("model %d belongs to another provider", id)
				}
				break
			}
		}
		refs = append(refs, model)
	}
	return refs, nil
}

func safeModelTestError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "任务已取消"
	case errors.Is(err, modelcatalog.ErrNVIDIAKeyRequired):
		return "需要选择一个 NVIDIA Key"
	case errors.Is(err, modelcatalog.ErrProviderNotConfigured):
		return "OpenCodeFree 渠道未配置"
	case errors.Is(err, modelcatalog.ErrProviderNotRoutable):
		return "该渠道尚未接入生产调用"
	case errors.Is(err, modelcatalog.ErrProviderMismatch):
		return "模型与测试渠道不匹配"
	}
	var proxyErr *xkproxy.Error
	if errors.As(err, &proxyErr) {
		return "上游代理暂不可用"
	}
	return "模型测试失败"
}

func isModelTestResultTerminal(status string) bool {
	switch status {
	case "success", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func dereferenceCredential(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func modelTestJobID(path string) (string, bool) {
	const prefix = "/admin/api/model-test-jobs/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	id := strings.TrimPrefix(path, prefix)
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return "", false
	}
	return id, true
}
