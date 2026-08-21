package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/modelhealth"
)

func TestModelHealthSummaryUsesSixHourAvailabilityDefaults(t *testing.T) {
	service := &modelHealthServiceFake{summary: modelhealth.Summary{Range: "6h"}}
	handler := NewModelHealth(service)
	response := performAdminRequest(handler, http.MethodGet, "/admin/api/model-health/summary", "")
	if response.Code != http.StatusOK {
		t.Fatalf("summary status = %d: %s", response.Code, response.Body.String())
	}
	if service.rangeName != "6h" || service.sortName != "availability" {
		t.Fatalf("summary query = %q/%q, want 6h/availability", service.rangeName, service.sortName)
	}
}

func TestModelHealthSummaryRejectsUnknownRangeAndSort(t *testing.T) {
	handler := NewModelHealth(&modelHealthServiceFake{})
	for _, path := range []string{
		"/admin/api/model-health/summary?range=90d",
		"/admin/api/model-health/summary?sort=latency",
	} {
		response := performAdminRequest(handler, http.MethodGet, path, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400", path, response.Code)
		}
	}
}

func TestModelHealthSettingsPatchValidatesAndWakesScheduler(t *testing.T) {
	service := &modelHealthServiceFake{settings: modelhealth.DefaultSettings()}
	handler := NewModelHealth(service)
	response := performAdminRequest(handler, http.MethodPatch, "/admin/api/model-health/settings", `{"enabled":true,"interval_seconds":300,"concurrency":4}`)
	if response.Code != http.StatusOK {
		t.Fatalf("valid settings status = %d: %s", response.Code, response.Body.String())
	}
	if service.settings.IntervalSeconds != 300 || service.settings.Concurrency != 4 || !service.settings.Enabled {
		t.Fatalf("updated settings = %+v", service.settings)
	}
	if service.runWakeCount != 1 {
		t.Fatalf("wake count = %d, want 1", service.runWakeCount)
	}

	response = performAdminRequest(handler, http.MethodPatch, "/admin/api/model-health/settings", `{"interval_seconds":9}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid settings status = %d, want 400", response.Code)
	}
}

func TestModelHealthRunReturnsAcceptedWithoutExposingErrors(t *testing.T) {
	service := &modelHealthServiceFake{}
	handler := NewModelHealth(service)
	response := performAdminRequest(handler, http.MethodPost, "/admin/api/model-health/run", "")
	if response.Code != http.StatusAccepted || service.runWakeCount != 1 {
		t.Fatalf("run response = %d/%d, want 202/1", response.Code, service.runWakeCount)
	}
	if strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("run response contains private details: %s", response.Body.String())
	}
}

func TestModelHealthMapsServiceFailureToGenericError(t *testing.T) {
	service := &modelHealthServiceFake{summaryErr: errors.New("private database detail")}
	handler := NewModelHealth(service)
	response := performAdminRequest(handler, http.MethodGet, "/admin/api/model-health/summary?range=6h", "")
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private database detail") {
		t.Fatalf("service error response = %d/%s", response.Code, response.Body.String())
	}
}

type modelHealthServiceFake struct {
	settings     modelhealth.Settings
	summary      modelhealth.Summary
	summaryErr   error
	rangeName    string
	sortName     string
	runWakeCount int
}

func (f *modelHealthServiceFake) Summary(_ context.Context, rangeName, sortName string) (modelhealth.Summary, error) {
	f.rangeName, f.sortName = rangeName, sortName
	if f.summaryErr != nil {
		return modelhealth.Summary{}, f.summaryErr
	}
	return f.summary, nil
}

func (f *modelHealthServiceFake) Settings(context.Context) (modelhealth.Settings, error) {
	if f.settings.IntervalSeconds == 0 {
		f.settings = modelhealth.DefaultSettings()
	}
	return f.settings, nil
}

func (f *modelHealthServiceFake) UpdateSettings(_ context.Context, settings modelhealth.Settings) (modelhealth.Settings, error) {
	if err := modelhealth.ValidateSettings(settings); err != nil {
		return modelhealth.Settings{}, err
	}
	f.settings = settings
	f.runWakeCount++
	return settings, nil
}

func (f *modelHealthServiceFake) PatchSettings(_ context.Context, patch modelhealth.SettingsPatch) (modelhealth.Settings, error) {
	current := f.settings
	if current.IntervalSeconds == 0 {
		current = modelhealth.DefaultSettings()
	}
	if patch.Enabled != nil {
		current.Enabled = *patch.Enabled
	}
	if patch.IntervalSeconds != nil {
		current.IntervalSeconds = *patch.IntervalSeconds
	}
	if patch.Concurrency != nil {
		current.Concurrency = *patch.Concurrency
	}
	if err := modelhealth.ValidateSettings(current); err != nil {
		return modelhealth.Settings{}, err
	}
	f.settings = current
	f.runWakeCount++
	return current, nil
}

func (f *modelHealthServiceFake) RunNow() {
	f.runWakeCount++
}

var _ = time.Time{}
