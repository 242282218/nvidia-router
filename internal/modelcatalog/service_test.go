package modelcatalog

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/database"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/upstream/nvidia"
)

func TestDiscoverCandidatesDoesNotMutateWhitelist(t *testing.T) {
	service, db, secrets, discoverer := newCatalogTestService(t)
	discoverer.models = []string{"model-b", "model-a"}

	candidates, err := service.DiscoverCandidates(context.Background(), 11)
	if err != nil {
		t.Fatalf("DiscoverCandidates: %v", err)
	}
	if got := candidateIDs(candidates); !reflect.DeepEqual(got, []string{"model-b", "model-a"}) {
		t.Fatalf("candidate IDs = %v", got)
	}
	if secrets.lastKeyID != 11 || discoverer.lastToken != "secret-for-11" {
		t.Fatalf("secret/discovery = %d/%q", secrets.lastKeyID, discoverer.lastToken)
	}
	assertModelCount(t, db, 0)

	selected := Selection{PublicID: "public-a", UpstreamID: "model-a", DisplayName: "Model A", Kind: KindChat, Enabled: true}
	if err := service.SaveSelection(context.Background(), []Selection{selected}); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}
	assertModelCount(t, db, 1)

	discoverer.models = []string{"new-model"}
	if _, err := service.DiscoverCandidates(context.Background(), 12); err != nil {
		t.Fatalf("DiscoverCandidates second key: %v", err)
	}
	assertModelCount(t, db, 1)
}

func TestWhitelistMapsPublicIDAndDisablesImmediately(t *testing.T) {
	service, _, _, _ := newCatalogTestService(t)
	selections := []Selection{
		{PublicID: "chat-public", UpstreamID: "vendor/chat", DisplayName: "Chat", Kind: KindChat, Enabled: true},
		{PublicID: "hidden", UpstreamID: "vendor/hidden", DisplayName: "Hidden", Kind: KindChat, Enabled: false},
	}
	if err := service.SaveSelection(context.Background(), selections); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}

	models, err := service.ListEnabled(context.Background())
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(models) != 1 || models[0].PublicID != "chat-public" {
		t.Fatalf("enabled models = %+v", models)
	}
	resolved, err := service.Resolve(context.Background(), "chat-public", Requirements{Kind: KindChat})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.UpstreamID != "vendor/chat" {
		t.Fatalf("upstream ID = %q", resolved.UpstreamID)
	}

	if err := service.SetEnabled(context.Background(), resolved.ID, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if _, err := service.Resolve(context.Background(), "chat-public", Requirements{Kind: KindChat}); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("Resolve disabled error = %v", err)
	}
}

func TestResolveEnforcesKindAndCapabilities(t *testing.T) {
	service, _, _, _ := newCatalogTestService(t)
	if err := service.SaveSelection(context.Background(), []Selection{
		{
			PublicID: "capable", UpstreamID: "vendor/capable", DisplayName: "Capable", Kind: KindChat, Enabled: true,
			SupportsVision: true, SupportsTools: true, SupportsReasoning: true, ReasoningWireFormat: "openai",
		},
		{PublicID: "plain", UpstreamID: "vendor/plain", DisplayName: "Plain", Kind: KindChat, Enabled: true},
		{PublicID: "embedding", UpstreamID: "vendor/embed", DisplayName: "Embedding", Kind: KindEmbedding, Enabled: true},
	}); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}

	for _, requirements := range []Requirements{
		{Kind: KindChat, Vision: true},
		{Kind: KindChat, Tools: true},
		{Kind: KindChat, Reasoning: true},
	} {
		if _, err := service.Resolve(context.Background(), "capable", requirements); err != nil {
			t.Fatalf("Resolve capable %+v: %v", requirements, err)
		}
		if _, err := service.Resolve(context.Background(), "plain", requirements); !errors.Is(err, ErrCapabilityUnsupported) {
			t.Fatalf("Resolve plain %+v error = %v", requirements, err)
		}
	}
	if _, err := service.Resolve(context.Background(), "embedding", Requirements{Kind: KindChat}); !errors.Is(err, ErrModelKindMismatch) {
		t.Fatalf("kind mismatch error = %v", err)
	}
}

func TestAudioModelsRequireVerificationBeforeEnable(t *testing.T) {
	service, _, _, _ := newCatalogTestService(t)
	for _, kind := range []Kind{KindASR, KindTTS} {
		selection := Selection{PublicID: string(kind), UpstreamID: "vendor/" + string(kind), DisplayName: string(kind), Kind: kind, Enabled: true}
		if err := service.SaveSelection(context.Background(), []Selection{selection}); !errors.Is(err, ErrCapabilityUnverified) {
			t.Fatalf("SaveSelection(%s) error = %v", kind, err)
		}

		verifiedAt := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
		selection.CapabilityVerifiedAt = &verifiedAt
		if err := service.SaveSelection(context.Background(), []Selection{selection}); err != nil {
			t.Fatalf("SaveSelection verified %s: %v", kind, err)
		}
		model, err := service.Resolve(context.Background(), string(kind), Requirements{Kind: kind})
		if err != nil {
			t.Fatalf("Resolve verified %s: %v", kind, err)
		}
		if err := service.SetCapabilityVerified(context.Background(), model.ID, nil); err != nil {
			t.Fatalf("SetCapabilityVerified nil: %v", err)
		}
		if err := service.SetEnabled(context.Background(), model.ID, true); !errors.Is(err, ErrCapabilityUnverified) {
			t.Fatalf("enable unverified %s error = %v", kind, err)
		}
	}
}

func TestChangingModelKindClearsModelBlocks(t *testing.T) {
	service, db, _, _ := newCatalogTestService(t)
	keyID := insertNVIDIAKey(t, db)
	if err := service.SaveSelection(context.Background(), []Selection{{PublicID: "kind-block", UpstreamID: "vendor/kind-block", DisplayName: "Kind Block", Kind: KindChat, Enabled: true}}); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}
	model, err := service.Resolve(context.Background(), "kind-block", Requirements{Kind: KindChat})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	status := 403
	if err := service.BlockKeyModel(context.Background(), keyID, model.ID, "model_forbidden", &status); err != nil {
		t.Fatalf("BlockKeyModel: %v", err)
	}
	if _, err := service.Patch(context.Background(), model.ID, Patch{Kind: kindPointer(KindEmbedding)}); err != nil {
		t.Fatalf("Patch kind: %v", err)
	}
	assertBlockCount(t, db, 0)
}

func TestChangingModelKindClearsCapabilityVerification(t *testing.T) {
	service, _, _, _ := newCatalogTestService(t)
	verifiedAt := time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC)
	if err := service.SaveSelection(context.Background(), []Selection{{
		PublicID: "speech", UpstreamID: "vendor/speech", DisplayName: "Speech",
		Kind: KindASR, CapabilityVerifiedAt: &verifiedAt,
	}}); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}
	models, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	model := models[0]

	chat, err := service.Patch(context.Background(), model.ID, Patch{Kind: kindPointer(KindChat)})
	if err != nil {
		t.Fatalf("Patch to chat: %v", err)
	}
	if chat.CapabilityVerifiedAt != nil {
		t.Fatalf("chat retained audio verification: %v", chat.CapabilityVerifiedAt)
	}
	asr, err := service.Patch(context.Background(), model.ID, Patch{Kind: kindPointer(KindASR)})
	if err != nil {
		t.Fatalf("Patch back to asr: %v", err)
	}
	if asr.CapabilityVerifiedAt != nil {
		t.Fatalf("asr regained stale verification: %v", asr.CapabilityVerifiedAt)
	}
	if err := service.SetEnabled(context.Background(), model.ID, true); !errors.Is(err, ErrCapabilityUnverified) {
		t.Fatalf("enable stale-verified asr error = %v", err)
	}
}

func kindPointer(kind Kind) *Kind { return &kind }

func TestManualUnblockUsesTargetChatEndpoint(t *testing.T) {
	service, db, _, discoverer := newCatalogTestService(t)
	keyID := insertNVIDIAKey(t, db)
	if err := service.SaveSelection(context.Background(), []Selection{{PublicID: "manual-chat", UpstreamID: "vendor/manual-chat", DisplayName: "Manual Chat", Kind: KindChat, Enabled: true}}); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}
	model, err := service.Resolve(context.Background(), "manual-chat", Requirements{Kind: KindChat})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	status := 403
	if err := service.BlockKeyModel(context.Background(), keyID, model.ID, "model_forbidden", &status); err != nil {
		t.Fatalf("BlockKeyModel: %v", err)
	}
	discoverer.chatResponse = `{"choices":[{"message":{"content":"ok"}}]}`
	if _, err := service.VerifyAndUnblock(context.Background(), keyID, model.ID); err != nil {
		t.Fatalf("VerifyAndUnblock: %v", err)
	}
	if discoverer.chatCalls != 1 || discoverer.modelsCalls != 0 {
		t.Fatalf("target calls chat/models = %d/%d, want 1/0", discoverer.chatCalls, discoverer.modelsCalls)
	}
	assertBlockCount(t, db, 0)
}

func TestVerifyAndUnblockAudioModelsMarksVerificationAndClearsBlock(t *testing.T) {
	service, db, _, discoverer := newCatalogTestService(t)
	keyID := insertNVIDIAKey(t, db)
	for _, kind := range []Kind{KindASR, KindTTS} {
		if err := service.SaveSelection(context.Background(), []Selection{{PublicID: string(kind), UpstreamID: "vendor/" + string(kind), DisplayName: string(kind), Kind: kind}}); err != nil {
			t.Fatalf("save %s: %v", kind, err)
		}
		model, err := service.repository.Get(context.Background(), modelIDByPublicID(t, db, string(kind)))
		if err != nil {
			t.Fatalf("get %s: %v", kind, err)
		}
		status := 403
		if err := service.BlockKeyModel(context.Background(), keyID, model.ID, "model_forbidden", &status); err != nil {
			t.Fatalf("block %s: %v", kind, err)
		}
		verified, err := service.VerifyAndUnblock(context.Background(), keyID, model.ID)
		if err != nil {
			t.Fatalf("verify %s: %v", kind, err)
		}
		if verified.CapabilityVerifiedAt == nil {
			t.Fatalf("%s missing capability verification", kind)
		}
		stored, err := service.repository.Get(context.Background(), model.ID)
		if err != nil || stored.CapabilityVerifiedAt == nil {
			t.Fatalf("stored %s verification = %v/%v", kind, stored.CapabilityVerifiedAt, err)
		}
		assertBlockCount(t, db, 0)
		if kind == KindASR && discoverer.asrCalls != 1 {
			t.Fatalf("ASR calls = %d", discoverer.asrCalls)
		}
		if kind == KindTTS && discoverer.ttsCalls != 1 {
			t.Fatalf("TTS calls = %d", discoverer.ttsCalls)
		}
	}
}

func TestVerifyAndUnblockAudioFailureDoesNotWriteVerification(t *testing.T) {
	service, db, _, discoverer := newCatalogTestService(t)
	keyID := insertNVIDIAKey(t, db)
	for _, kind := range []Kind{KindASR, KindTTS} {
		publicID := "failed-" + string(kind)
		if err := service.SaveSelection(context.Background(), []Selection{{PublicID: publicID, UpstreamID: "vendor/" + string(kind), DisplayName: string(kind), Kind: kind}}); err != nil {
			t.Fatalf("save %s: %v", kind, err)
		}
		modelID := modelIDByPublicID(t, db, publicID)
		status := 403
		if err := service.BlockKeyModel(context.Background(), keyID, modelID, "model_forbidden", &status); err != nil {
			t.Fatalf("block %s: %v", kind, err)
		}
		if kind == KindASR {
			discoverer.asrResponse = `{"error":"bad"}`
		} else {
			discoverer.ttsResponse = ""
			discoverer.ttsResponseSet = true
		}
		if _, err := service.VerifyAndUnblock(context.Background(), keyID, modelID); !errors.Is(err, ErrManualTestRequired) {
			t.Fatalf("verify %s error = %v", kind, err)
		}
		stored, err := service.repository.Get(context.Background(), modelID)
		if err != nil {
			t.Fatalf("get failed %s: %v", kind, err)
		}
		if stored.CapabilityVerifiedAt != nil {
			t.Fatalf("failed %s wrote verification %v", kind, stored.CapabilityVerifiedAt)
		}
		assertBlockCount(t, db, 1)
		if err := service.UnblockKeyModel(context.Background(), keyID, modelID, true); err != nil {
			t.Fatalf("cleanup %s: %v", kind, err)
		}
	}
}

func modelIDByPublicID(t *testing.T, db *sql.DB, publicID string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow("SELECT id FROM models WHERE public_id = ?", publicID).Scan(&id); err != nil {
		t.Fatalf("model ID %s: %v", publicID, err)
	}
	return id
}

func TestModelsListingCannotVerifyAudioEndpointCapability(t *testing.T) {
	service, db, _, discoverer := newCatalogTestService(t)
	keyID := insertNVIDIAKey(t, db)

	for _, kind := range []Kind{KindASR, KindTTS} {
		upstreamID := "vendor/" + string(kind)
		if err := service.SaveSelection(context.Background(), []Selection{{
			PublicID: string(kind), UpstreamID: upstreamID, DisplayName: string(kind), Kind: kind,
		}}); err != nil {
			t.Fatalf("SaveSelection(%s): %v", kind, err)
		}
		models, err := service.List(context.Background())
		if err != nil {
			t.Fatalf("List(%s): %v", kind, err)
		}
		var model Model
		for _, item := range models {
			if item.PublicID == string(kind) {
				model = item
				break
			}
		}
		status := 403
		if err := service.BlockKeyModel(context.Background(), keyID, model.ID, "model_forbidden", &status); err != nil {
			t.Fatalf("BlockKeyModel(%s): %v", kind, err)
		}
		discoverer.models = []string{upstreamID}
		if _, err := service.DiscoverCandidates(context.Background(), keyID); err != nil {
			t.Fatalf("DiscoverCandidates(%s): %v", kind, err)
		}
		// Discovery alone is not the audit verification path. A successful
		// endpoint probe is required before capability_verified_at is written.
		stored, err := service.repository.Get(context.Background(), model.ID)
		if err != nil {
			t.Fatalf("Get(%s): %v", kind, err)
		}
		if stored.CapabilityVerifiedAt != nil {
			t.Fatalf("%s capability verified by /models discovery: %v", kind, stored.CapabilityVerifiedAt)
		}
		assertBlockCount(t, db, 1)

		if err := service.UnblockKeyModel(context.Background(), keyID, model.ID, true); err != nil {
			t.Fatalf("cleanup block(%s): %v", kind, err)
		}
	}
}

func TestBlockUpsertsAndOnlySuccessfulManualTestCanUnblock(t *testing.T) {
	service, db, _, _ := newCatalogTestService(t)
	keyID := insertNVIDIAKey(t, db)
	if err := service.SaveSelection(context.Background(), []Selection{
		{PublicID: "blocked", UpstreamID: "vendor/blocked", DisplayName: "Blocked", Kind: KindChat, Enabled: true},
	}); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}
	model, err := service.Resolve(context.Background(), "blocked", Requirements{Kind: KindChat})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	status403 := 403
	if err := service.BlockKeyModel(context.Background(), keyID, model.ID, "model_forbidden", &status403); err != nil {
		t.Fatalf("BlockKeyModel: %v", err)
	}
	status404 := 404
	if err := service.BlockKeyModel(context.Background(), keyID, model.ID, "model_not_found", &status404); err != nil {
		t.Fatalf("BlockKeyModel upsert: %v", err)
	}
	assertBlock(t, db, keyID, model.ID, "model_not_found", 404)

	if err := service.UnblockKeyModel(context.Background(), keyID, model.ID, false); !errors.Is(err, ErrManualTestRequired) {
		t.Fatalf("Unblock without successful test error = %v", err)
	}
	assertBlockCount(t, db, 1)
	if err := service.UnblockKeyModel(context.Background(), keyID, model.ID, true); err != nil {
		t.Fatalf("Unblock after successful test: %v", err)
	}
	assertBlockCount(t, db, 0)

	if err := service.BlockKeyModel(context.Background(), keyID, model.ID, "model_forbidden", &status403); err != nil {
		t.Fatalf("BlockKeyModel before delete: %v", err)
	}
	if err := service.DeleteModel(context.Background(), model.ID); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	assertBlockCount(t, db, 0)

	if err := service.SaveSelection(context.Background(), []Selection{
		{PublicID: "cascade-key", UpstreamID: "vendor/cascade", DisplayName: "Cascade", Kind: KindChat, Enabled: true},
	}); err != nil {
		t.Fatalf("SaveSelection cascade key: %v", err)
	}
	cascadeModel, err := service.Resolve(context.Background(), "cascade-key", Requirements{Kind: KindChat})
	if err != nil {
		t.Fatalf("Resolve cascade key model: %v", err)
	}
	if err := service.BlockKeyModel(context.Background(), keyID, cascadeModel.ID, "model_forbidden", &status403); err != nil {
		t.Fatalf("BlockKeyModel before key delete: %v", err)
	}
	if _, err := db.Exec("DELETE FROM nvidia_keys WHERE id = ?", keyID); err != nil {
		t.Fatalf("delete NVIDIA key: %v", err)
	}
	assertBlockCount(t, db, 0)
}

func newCatalogTestService(t *testing.T) (*Service, *sql.DB, *fakeSecrets, *fakeDiscoverer) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "router.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	secrets := &fakeSecrets{}
	discoverer := &fakeDiscoverer{}
	return NewService(NewRepository(db), secrets, discoverer, nvidia.DefaultDescriptor(), catalogClock{}), db, secrets, discoverer
}

type fakeSecrets struct{ lastKeyID int64 }

func (s *fakeSecrets) WithSecret(_ context.Context, keyID int64, callback func([]byte) error) error {
	s.lastKeyID = keyID
	secret := []byte("secret-for-" + strconv.FormatInt(keyID, 10))
	return callback(secret)
}

type fakeDiscoverer struct {
	models         []string
	lastToken      string
	modelsCalls    int
	chatCalls      int
	chatResponse   string
	asrCalls       int
	ttsCalls       int
	asrResponse    string
	ttsResponse    string
	ttsResponseSet bool
	lastAudioModel string
}

func (d *fakeDiscoverer) Models(_ context.Context, token string) ([]string, error) {
	d.lastToken = token
	d.modelsCalls++
	return append([]string(nil), d.models...), nil
}

func (d *fakeDiscoverer) Chat(_ context.Context, _ runtimeconfig.Snapshot, _ string, _ []byte, _ bool) (*http.Response, error) {
	d.chatCalls++
	body := d.chatResponse
	if body == "" {
		body = `{"choices":[]}`
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

func (d *fakeDiscoverer) AudioTranscriptions(_ context.Context, _ runtimeconfig.Snapshot, _ string, body []byte, contentType string) (*http.Response, error) {
	d.asrCalls++
	if !strings.Contains(contentType, "multipart/form-data") || !bytes.Contains(body, []byte("vendor/asr")) || !bytes.Contains(body, []byte("RIFF")) {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"error":"invalid probe"}`)), Header: make(http.Header)}, nil
	}
	response := d.asrResponse
	if response == "" {
		response = `{"text":"ok"}`
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
}

func (d *fakeDiscoverer) AudioSpeech(_ context.Context, _ runtimeconfig.Snapshot, _ string, body []byte) (*http.Response, error) {
	d.ttsCalls++
	if !bytes.Contains(body, []byte("vendor/tts")) || !bytes.Contains(body, []byte("Magpie-Multilingual.EN-US.Aria")) {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"error":"invalid probe"}`)), Header: make(http.Header)}, nil
	}
	response := d.ttsResponse
	if !d.ttsResponseSet {
		response = "audio"
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
}

type catalogClock struct{}

func (catalogClock) Now() time.Time                              { return time.Date(2026, 7, 30, 4, 0, 0, 0, time.UTC) }
func (catalogClock) NewTimer(duration time.Duration) *time.Timer { return time.NewTimer(duration) }
func (catalogClock) AfterFunc(duration time.Duration, callback func()) *time.Timer {
	return time.AfterFunc(duration, callback)
}

func candidateIDs(candidates []Candidate) []string {
	ids := make([]string, len(candidates))
	for index, candidate := range candidates {
		ids[index] = candidate.UpstreamID
	}
	return ids
}

func assertModelCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM models").Scan(&count); err != nil {
		t.Fatalf("count models: %v", err)
	}
	if count != want {
		t.Fatalf("model count = %d, want %d", count, want)
	}
}

func insertNVIDIAKey(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	result, err := db.Exec(`
		INSERT INTO nvidia_keys (
			ciphertext, nonce, fingerprint, display_prefix, display_suffix, created_at, updated_at
		) VALUES (x'01', x'02', x'03', 'prefix', 'suffix', '2026-07-30T04:00:00Z', '2026-07-30T04:00:00Z')
	`)
	if err != nil {
		t.Fatalf("insert NVIDIA key: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("NVIDIA key ID: %v", err)
	}
	return id
}

func assertBlock(t *testing.T, db *sql.DB, keyID, modelID int64, wantReason string, wantStatus int) {
	t.Helper()
	var reason string
	var status int
	if err := db.QueryRow(`
		SELECT reason_code, upstream_status
		FROM nvidia_key_model_blocks
		WHERE nvidia_key_id = ? AND model_id = ?
	`, keyID, modelID).Scan(&reason, &status); err != nil {
		t.Fatalf("query block: %v", err)
	}
	if reason != wantReason || status != wantStatus {
		t.Fatalf("block = %q/%d, want %q/%d", reason, status, wantReason, wantStatus)
	}
}

func assertBlockCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM nvidia_key_model_blocks").Scan(&count); err != nil {
		t.Fatalf("count blocks: %v", err)
	}
	if count != want {
		t.Fatalf("block count = %d, want %d", count, want)
	}
}

func TestListIncludesBlockedNVIDIAKeyIDs(t *testing.T) {
	service, db, _, _ := newCatalogTestService(t)
	firstKeyID := insertNVIDIAKey(t, db)
	secondResult, err := db.Exec(`
		INSERT INTO nvidia_keys (
			ciphertext, nonce, fingerprint, display_prefix, display_suffix, created_at, updated_at
		) VALUES (x'04', x'05', x'06', 'prefix2', 'suffix2', '2026-07-30T04:00:00Z', '2026-07-30T04:00:00Z')
	`)
	if err != nil {
		t.Fatalf("insert second NVIDIA key: %v", err)
	}
	secondKeyID, err := secondResult.LastInsertId()
	if err != nil {
		t.Fatalf("second NVIDIA key ID: %v", err)
	}
	if err := service.SaveSelection(context.Background(), []Selection{{
		PublicID: "blocked-list", UpstreamID: "vendor/blocked-list", DisplayName: "Blocked List", Kind: KindChat, Enabled: true,
	}}); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}
	models, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List before block: %v", err)
	}
	modelID := models[0].ID
	status := 403
	if err := service.BlockKeyModel(context.Background(), secondKeyID, modelID, "model_forbidden", &status); err != nil {
		t.Fatalf("Block second key: %v", err)
	}
	if err := service.BlockKeyModel(context.Background(), firstKeyID, modelID, "model_forbidden", &status); err != nil {
		t.Fatalf("Block first key: %v", err)
	}

	models, err = service.List(context.Background())
	if err != nil {
		t.Fatalf("List after block: %v", err)
	}
	if got := models[0].BlockedByKeyIDs; !reflect.DeepEqual(got, []int64{firstKeyID, secondKeyID}) {
		t.Fatalf("BlockedByKeyIDs = %v, want [%d %d]", got, firstKeyID, secondKeyID)
	}
}
