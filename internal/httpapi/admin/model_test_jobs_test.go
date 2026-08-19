package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/modelcatalog"
)

type modelTestJobRunnerFake struct {
	models []modelcatalog.Model
}

func (f *modelTestJobRunnerFake) List(context.Context) ([]modelcatalog.Model, error) {
	return append([]modelcatalog.Model(nil), f.models...), nil
}

func (*modelTestJobRunnerFake) TestModel(context.Context, string, int64, int64) error { return nil }

func TestModelTestJobResponseUsesLowercasePublicFieldsAndOmitsCredentialID(t *testing.T) {
	handler := NewModelTestJobs(&modelTestJobRunnerFake{models: []modelcatalog.Model{{ID: 1, PublicID: "model"}}})
	response := performAdminRequest(handler, http.MethodPost, "/admin/api/model-test-jobs", `{"provider":"nvidia","credential_id":7,"model_ids":[1],"mode":"sequential","concurrency":1}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode job response: %v", err)
	}
	for _, field := range []string{"id", "provider", "mode", "status", "results"} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("job response missing %q: %s", field, response.Body.String())
		}
	}
	for _, forbidden := range []string{"ID", "Provider", "CredentialID", "credential_id", "Cancel", "Context", "CancelRequested"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("job response exposes internal field %q: %s", forbidden, response.Body.String())
		}
	}
}

type modelTestJobRunnerWithChecks struct {
	*modelTestJobRunnerFake
	configured    bool
	credentialErr error
}

func (f *modelTestJobRunnerWithChecks) OpenCodeFreeConfigured() bool { return f.configured }

func (f *modelTestJobRunnerWithChecks) ValidateNVIDIAKey(context.Context, int64) error {
	return f.credentialErr
}

func TestModelTestJobRejectsUnconfiguredOpenCodeFreeBeforeCreatingTask(t *testing.T) {
	runner := &modelTestJobRunnerWithChecks{modelTestJobRunnerFake: &modelTestJobRunnerFake{
		models: []modelcatalog.Model{{ID: 1, PublicID: "opencodefree/model", Provider: modelcatalog.ProviderOpenCodeFree}},
	}}
	handler := NewModelTestJobs(runner)
	response := performAdminRequest(handler, http.MethodPost, "/admin/api/model-test-jobs", `{"provider":"opencodefree","model_ids":[1],"mode":"sequential","concurrency":1}`)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "provider_not_configured") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestModelTestJobRejectsUnknownNVIDIACredentialBeforeCreatingTask(t *testing.T) {
	runner := &modelTestJobRunnerWithChecks{
		modelTestJobRunnerFake: &modelTestJobRunnerFake{models: []modelcatalog.Model{{ID: 1, PublicID: "model"}}},
		credentialErr:          errors.New("credential not found"),
	}
	handler := NewModelTestJobs(runner)
	response := performAdminRequest(handler, http.MethodPost, "/admin/api/model-test-jobs", `{"provider":"nvidia","credential_id":7,"model_ids":[1],"mode":"sequential","concurrency":1}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_credential") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

type blockingModelTestJobRunner struct {
	*modelTestJobRunnerFake
	started chan struct{}
	invoked chan struct{}
	mu      sync.Mutex
	count   int
}

func (f *blockingModelTestJobRunner) TestModel(ctx context.Context, _ string, _ int64, _ int64) error {
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
		response := performAdminRequest(handler, http.MethodPost, "/admin/api/model-test-jobs", `{"provider":"nvidia","credential_id":7,"model_ids":[1],"mode":"sequential","concurrency":1}`)
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
	response := performAdminRequest(handler, http.MethodPost, "/admin/api/model-test-jobs", `{"provider":"nvidia","credential_id":7,"model_ids":[1],"mode":"sequential","concurrency":1}`)
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
