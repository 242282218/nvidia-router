package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/modelcatalog"
)

type modelTestJobRunnerFake struct {
	models []modelcatalog.Model
	mu     sync.Mutex
	tested []int64
	fail   map[int64]error
}

func (f *modelTestJobRunnerFake) List(context.Context) ([]modelcatalog.Model, error) {
	return append([]modelcatalog.Model(nil), f.models...), nil
}

func (f *modelTestJobRunnerFake) TestModelAuto(_ context.Context, modelID int64) error {
	f.mu.Lock()
	f.tested = append(f.tested, modelID)
	f.mu.Unlock()
	return f.fail[modelID]
}

func (f *modelTestJobRunnerFake) testedIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.tested...)
}

func TestModelTestJobResponseUsesLowercasePublicFieldsAndOmitsCredentialID(t *testing.T) {
	handler := NewModelTestJobs(&modelTestJobRunnerFake{models: []modelcatalog.Model{{ID: 1, PublicID: "model"}}})
	response := performAdminRequest(handler, http.MethodPost, "/admin/api/model-test-jobs", `{"model_ids":[1],"mode":"sequential","concurrency":1}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	for _, field := range []string{"id", "mode", "status", "results"} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("job response missing %q: %s", field, response.Body.String())
		}
	}
	for _, forbidden := range []string{"ID", "Provider", "provider", "CredentialID", "credential_id", "Cancel", "Context", "CancelRequested"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("job response exposes internal field %q: %s", forbidden, response.Body.String())
		}
	}
}

// A single job may mix providers now: the probe dispatches on each model's own
// provider, so the operator never picks a channel.
func TestModelTestJobRunsMixedProvidersAndReportsProviderPerResult(t *testing.T) {
	runner := &modelTestJobRunnerFake{
		models: []modelcatalog.Model{
			{ID: 1, PublicID: "nvidia/model", Provider: modelcatalog.ProviderNVIDIA},
			{ID: 2, PublicID: "opencodefree/model", Provider: modelcatalog.ProviderOpenCodeFree},
		},
		fail: map[int64]error{2: modelcatalog.ErrUpstreamUnreachable},
	}
	handler := NewModelTestJobs(runner)
	created := performAdminRequest(handler, http.MethodPost, "/admin/api/model-test-jobs", `{"model_ids":[1,2],"mode":"sequential"}`)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}

	final := awaitModelTestJob(t, handler, job.ID)
	if final.Status != "failed" {
		t.Fatalf("job status = %q, want failed", final.Status)
	}
	if len(final.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(final.Results))
	}
	if final.Results[0].Provider != modelcatalog.ProviderNVIDIA || final.Results[0].Status != "success" {
		t.Fatalf("nvidia result = %+v", final.Results[0])
	}
	if final.Results[1].Provider != modelcatalog.ProviderOpenCodeFree || final.Results[1].Status != "failed" {
		t.Fatalf("opencodefree result = %+v", final.Results[1])
	}
	if final.Results[1].Error != "上游多次未返回可用响应" {
		t.Fatalf("opencodefree error = %q", final.Results[1].Error)
	}
	if tested := runner.testedIDs(); len(tested) != 2 {
		t.Fatalf("tested models = %v, want both", tested)
	}
}

func TestModelTestJobRejectsUnknownModel(t *testing.T) {
	handler := NewModelTestJobs(&modelTestJobRunnerFake{models: []modelcatalog.Model{{ID: 1, PublicID: "model"}}})
	response := performAdminRequest(handler, http.MethodPost, "/admin/api/model-test-jobs", `{"model_ids":[99],"mode":"sequential"}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "The selected models are invalid.") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func awaitModelTestJob(t *testing.T, handler *ModelTestJobs, id string) modelTestJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response := performAdminRequest(handler, http.MethodGet, "/admin/api/model-test-jobs/"+id, "")
		if response.Code != http.StatusOK {
			t.Fatalf("get job status = %d, body = %s", response.Code, response.Body.String())
		}
		var snapshot modelTestJob
		if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
			t.Fatalf("decode job snapshot: %v", err)
		}
		if snapshot.Status != "queued" && snapshot.Status != "running" {
			return snapshot
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("job %s did not finish, status = %q", id, snapshot.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type blockingModelTestJobRunner struct {
	*modelTestJobRunnerFake
	started chan struct{}
	invoked chan struct{}
	mu      sync.Mutex
	count   int
}

func (f *blockingModelTestJobRunner) TestModelAuto(ctx context.Context, _ int64) error {
	f.mu.Lock()
	f.count++
	f.mu.Unlock()
	f.invoked <- struct{}{}
	select {
	case <-f.started:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func TestModelTestJobsLimitActiveTasks(t *testing.T) {
	runner := &blockingModelTestJobRunner{
		modelTestJobRunnerFake: &modelTestJobRunnerFake{models: []modelcatalog.Model{{ID: 1, PublicID: "model"}}},
		started:                make(chan struct{}),
		invoked:                make(chan struct{}, modelTestMaxActiveJobs),
	}
	handler := NewModelTestJobs(runner)
	ids := make([]string, 0, modelTestMaxActiveJobs)
	for index := 0; index < modelTestMaxActiveJobs; index++ {
		response := performAdminRequest(handler, http.MethodPost, "/admin/api/model-test-jobs", `{"model_ids":[1],"mode":"sequential","concurrency":1}`)
		if response.Code != http.StatusAccepted {
			t.Fatalf("job %d status = %d, body = %s", index, response.Code, response.Body.String())
		}
		var job struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, job.ID)
	}
	response := performAdminRequest(handler, http.MethodPost, "/admin/api/model-test-jobs", `{"model_ids":[1],"mode":"sequential","concurrency":1}`)
	if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), "model_test_capacity") {
		t.Fatalf("capacity status = %d, body = %s", response.Code, response.Body.String())
	}
	for index := 0; index < modelTestMaxActiveJobs; index++ {
		select {
		case <-runner.invoked:
		case <-time.After(5 * time.Second):
			t.Fatalf("active job %d did not start", index+1)
		}
	}
	for _, id := range ids {
		cancelled := performAdminRequest(handler, http.MethodDelete, "/admin/api/model-test-jobs/"+id, "")
		if cancelled.Code != http.StatusOK {
			t.Fatalf("cancel %s status = %d, body = %s", id, cancelled.Code, cancelled.Body.String())
		}
	}
	runner.mu.Lock()
	count := runner.count
	runner.mu.Unlock()
	if count < modelTestMaxActiveJobs {
		t.Fatalf("started jobs = %d, want at least %d", count, modelTestMaxActiveJobs)
	}
}
