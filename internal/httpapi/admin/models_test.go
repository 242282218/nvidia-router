package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/modelcatalog"
)

type fakeModels struct {
	candidates  []modelcatalog.Candidate
	models      []modelcatalog.Model
	saved       []modelcatalog.Selection
	patched     modelcatalog.Selection
	discoverKey int64
	cleared     [2]int64
}

func (f *fakeModels) DiscoverCandidates(_ context.Context, keyID int64) ([]modelcatalog.Candidate, error) {
	f.discoverKey = keyID
	return f.candidates, nil
}
func (f *fakeModels) SaveSelection(_ context.Context, s []modelcatalog.Selection) error {
	f.saved = append([]modelcatalog.Selection(nil), s...)
	return nil
}
func (f *fakeModels) List(context.Context) ([]modelcatalog.Model, error) {
	return append([]modelcatalog.Model(nil), f.models...), nil
}
func (f *fakeModels) Patch(_ context.Context, id int64, p modelcatalog.Patch) (modelcatalog.Model, error) {
	for _, m := range f.models {
		if m.ID == id {
			if p.Enabled != nil && *p.Enabled && (m.Kind == modelcatalog.KindASR || m.Kind == modelcatalog.KindTTS) && m.CapabilityVerifiedAt == nil {
				return modelcatalog.Model{}, modelcatalog.ErrCapabilityUnverified
			}
			if p.Enabled != nil {
				m.Enabled = *p.Enabled
			}
			return m, nil
		}
	}
	return modelcatalog.Model{}, modelcatalog.ErrModelNotFound
}
func (f *fakeModels) VerifyAndUnblock(_ context.Context, keyID, modelID int64) (modelcatalog.Model, error) {
	f.cleared = [2]int64{keyID, modelID}
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	return modelcatalog.Model{ID: modelID, CapabilityVerifiedAt: &now}, nil
}

type fakeCandidateKeys struct {
	id  int64
	err error
}

func (f fakeCandidateKeys) FirstEnabledID(context.Context) (int64, error) { return f.id, f.err }

func TestModelAPIUsesFirstKeyAndEnforcesAudioVerification(t *testing.T) {
	models := &fakeModels{candidates: []modelcatalog.Candidate{{UpstreamID: "vendor/model", DisplayName: "Model", Kind: modelcatalog.KindChat}}, models: []modelcatalog.Model{{ID: 9, PublicID: "speech", UpstreamID: "vendor/speech", DisplayName: "Speech", Kind: modelcatalog.KindTTS, BlockedByKeyIDs: []int64{5}}}}
	syncer := &fakeStateSync{}
	handler := NewModels(models, fakeCandidateKeys{id: 5}, syncer)
	listResponse := performAdminRequest(handler, http.MethodGet, "/admin/api/models", "")
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"blocked_by_key_ids":[5]`) {
		t.Fatalf("model list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	response := performAdminRequest(handler, http.MethodGet, "/admin/api/models/candidates", "")
	if response.Code != http.StatusOK || models.discoverKey != 5 || !strings.Contains(response.Body.String(), "vendor/model") {
		t.Fatalf("candidates status=%d key=%d body=%s", response.Code, models.discoverKey, response.Body.String())
	}
	response = performAdminRequest(handler, http.MethodPatch, "/admin/api/models/9", `{"enabled":true}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "capability_unverified") {
		t.Fatalf("audio gate status=%d body=%s", response.Code, response.Body.String())
	}
	response = performAdminRequest(handler, http.MethodDelete, "/admin/api/key-model-blocks/5/9", "")
	if response.Code != http.StatusOK || models.cleared != [2]int64{5, 9} || len(syncer.blocks) != 1 || syncer.blocks[0] != [3]int64{5, 9, 0} {
		t.Fatalf("unblock status=%d cleared=%v blocks=%v body=%s", response.Code, models.cleared, syncer.blocks, response.Body.String())
	}
}

func TestModelCandidatesReportsMissingKeyWithoutSecret(t *testing.T) {
	handler := NewModels(&fakeModels{}, fakeCandidateKeys{err: errors.New("no key")}, &fakeStateSync{})
	response := performAdminRequest(handler, http.MethodGet, "/admin/api/models/candidates", "")
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "no key") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
