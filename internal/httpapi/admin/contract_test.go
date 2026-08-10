package admin

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nvidia-router/internal/accesskey"
	"nvidia-router/internal/modelcatalog"
	"nvidia-router/internal/nvidiakey"
	"nvidia-router/internal/observability"
	"nvidia-router/internal/pool"
	"nvidia-router/internal/runtimeconfig"
	"nvidia-router/internal/xkproxy"
)

// updateSnapshots rewrites testdata/contract_*.json. It is intentionally a
// flag rather than an env var so `go test ./internal/httpapi/admin -update`
// is the one obvious way to regenerate after a deliberate contract change; a
// plain run compares against the committed snapshot and fails with a diff.
var updateSnapshots = flag.Bool("update", false, "rewrite testdata/contract_*.json snapshots")

// TestAdminAPIContractSnapshots serves every admin endpoint the front-end
// consumes and pins the exact JSON wire shape. The snapshots are the single
// source of truth for web/src/shared/api/contract.spec.ts, which parses each
// file with the real front-end type guards — a backend field rename or a
// front-end type drift fails here (diff) or there (guard) instead of silently
// desyncing. This is the audit R13/R3.1 regression net.
func TestAdminAPIContractSnapshots(t *testing.T) {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	settingsStore := &fakeRuntimeSettingsStore{snapshot: contractSettingsSnapshot()}
	models := &fakeModels{models: contractModels(), candidates: contractCandidates()}
	accessKeys := &fakeAccessKeys{keys: contractAccessKeys(), created: accesskey.CreatedKey{
		Key:       accesskey.Key{ID: 4, Name: "ci", Prefix: "nvr_ci", CreatedAt: now, RPMLimit: 60, TPMLimit: 10000, MaxConcurrent: 4},
		Plaintext: "nvr_once_only_secret",
	}}
	nvidiaKeys := &fakeNVIDIAKeys{keys: contractNVIDIAKeys()}
	proxyPool := &fakeProxyPoolService{snapshot: xkproxy.Snapshot{
		Enabled: true, ProxyURL: "http://proxy-pool:8080", AuthConfigured: true, Source: xkproxy.SourceDatabase,
	}}
	runtimeProvider := fakeRuntimeSummary{summary: pool.Summary{
		Keys:             pool.KeyStatusCounts{Total: 3, Enabled: 2, Disabled: 1, CoolingDown: 1, Ready: 1},
		Active:           2,
		Queue:            pool.QueueSummary{Length: 4, Capacity: 9},
		EarliestCooldown: timePointer(now.Add(2 * time.Hour)),
	}}
	monitoring := &monitoringStoreStub{summary: contractMonitoringSnapshot(), logs: contractMonitoringLogs()}

	tests := []struct {
		name    string
		handler http.Handler
		method  string
		path    string
		body    string
	}{
		{
			name:    "settings_get",
			handler: NewSettings(settingsStore),
			method:  http.MethodGet, path: "/admin/api/settings",
		},
		{
			name:    "settings_patch",
			handler: NewSettings(settingsStore),
			method:  http.MethodPatch, path: "/admin/api/settings",
			body: `{"queue_capacity":9,"first_byte_timeout_ms":12000}`,
		},
		{
			name:    "models_get",
			handler: NewModels(models, fakeCandidateKeys{id: 7}, &modelSyncFake{}),
			method:  http.MethodGet, path: "/admin/api/models",
		},
		{
			name:    "models_candidates_get",
			handler: NewModels(models, fakeCandidateKeys{id: 7}, &modelSyncFake{}),
			method:  http.MethodGet, path: "/admin/api/models/candidates",
		},
		{
			name:    "models_post",
			handler: NewModels(models, fakeCandidateKeys{id: 7}, &modelSyncFake{}),
			method:  http.MethodPost, path: "/admin/api/models",
			body: `{"models":[{"public_id":"chat-llm","upstream_id":"meta/llama-3.3-70b","display_name":"Chat LLM","kind":"chat","enabled":true,"supports_vision":true,"supports_tools":true,"supports_reasoning":true,"reasoning_wire_format":"chain_of_thought"}]}`,
		},
		{
			name:    "models_patch",
			handler: NewModels(models, fakeCandidateKeys{id: 7}, &modelSyncFake{}),
			method:  http.MethodPatch, path: "/admin/api/models/2",
			body: `{"enabled":true,"supports_tools":true}`,
		},
		{
			name:    "access_keys_get",
			handler: NewAccessKeys(accessKeys),
			method:  http.MethodGet, path: "/admin/api/access-keys",
		},
		{
			name:    "access_keys_post",
			handler: NewAccessKeys(accessKeys),
			method:  http.MethodPost, path: "/admin/api/access-keys",
			body: `{"name":"ci"}`,
		},
		{
			name:    "nvidia_keys_get",
			handler: NewNVIDIAKeys(nvidiaKeys, &fakeStateSync{}),
			method:  http.MethodGet, path: "/admin/api/nvidia-keys",
		},
		{
			name:    "proxy_pool_get",
			handler: NewProxyPool(proxyPool),
			method:  http.MethodGet, path: "/admin/api/proxy-pool",
		},
		{
			name:    "runtime_summary_get",
			handler: NewRuntime(runtimeProvider),
			method:  http.MethodGet, path: "/admin/api/runtime/summary",
		},
		{
			name:    "monitoring_summary_get",
			handler: NewMonitoring(monitoring, fixedAdminClock{now: now}),
			method:  http.MethodGet, path: "/admin/api/monitoring/summary?range=24h",
		},
		{
			name:    "monitoring_logs_get",
			handler: NewMonitoring(monitoring, fixedAdminClock{now: now}),
			method:  http.MethodGet, path: "/admin/api/monitoring/logs?range=7d&page=1&page_size=50",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performAdminRequest(test.handler, test.method, test.path, test.body)
			if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
				t.Fatalf("%s %s status = %d, want 2xx: %s", test.method, test.path, response.Code, response.Body.String())
			}
			contractSnapshot(t, test.name, response.Body.Bytes())
		})
	}
}

// contractSnapshot compares the captured body against testdata/contract_<name>.json,
// rewriting the golden file when -update is set and otherwise failing with a
// line diff so a contract drift names the changed JSON path immediately.
func contractSnapshot(t *testing.T, name string, body []byte) {
	t.Helper()
	formatted := formatSnapshotJSON(body)
	path := filepath.Join("testdata", "contract_"+name+".json")
	if *updateSnapshots {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("create testdata: %v", err)
		}
		if err := os.WriteFile(path, formatted, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing snapshot %s — regenerate with `go test ./internal/httpapi/admin -update`: %v", path, err)
	}
	if bytes.Equal(want, formatted) {
		return
	}
	t.Fatalf("contract %s drifted; run `go test ./internal/httpapi/admin -update` to accept the new shape\ndiff (-golden +actual):\n%s", name, diffLines(string(want), string(formatted)))
}

// formatSnapshotJSON re-encodes the response body with a stable indent so
// snapshots never depend on Go map-iteration order or encoder formatting.
func formatSnapshotJSON(body []byte) []byte {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return body
	}
	formatted, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return body
	}
	return append(formatted, '\n')
}

// diffLines is a compact LCS-based line diff (O(n*m)) for the bounded snapshot
// files; it labels removals from the golden file with "-" and additions from
// the actual response with "+".
func diffLines(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	n, m := len(wantLines), len(gotLines)
	dp := make([][]int, n+1)
	for index := range dp {
		dp[index] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case wantLines[i] == gotLines[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var builder strings.Builder
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && wantLines[i] == gotLines[j]:
			builder.WriteString("  " + wantLines[i] + "\n")
			i++
			j++
		case i < n && (j == m || dp[i+1][j] >= dp[i][j+1]):
			builder.WriteString("- " + wantLines[i] + "\n")
			i++
		default:
			builder.WriteString("+ " + gotLines[j] + "\n")
			j++
		}
	}
	return builder.String()
}

func contractSettingsSnapshot() runtimeconfig.Snapshot {
	return runtimeconfig.Snapshot{
		QueueCapacity: 100, QueueWaitTimeoutMS: 60000, ConnectTimeoutMS: 10000,
		FirstByteTimeoutMS: 60000, NonstreamTotalTimeoutMS: 300000, ShutdownGraceMS: 60000,
		FailoverStatusCodes: "429,500,502,503,504", RequestLogRetentionDays: 30,
		MaxAttemptsPerRequest: 5, RetryBudgetMS: 120000, MaxStreamingPerKey: 2,
		StreamFirstTokenTimeoutMS: 60000, StreamIdleTimeoutMS: 180000,
	}
}

func contractModels() []modelcatalog.Model {
	verified := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	// BlockedByKeyIDs must mirror the repository, which always hands back a
	// non-nil slice so the wire carries [] rather than null (audit R13: the
	// front-end guard rejects null for this field).
	return []modelcatalog.Model{
		{ID: 1, PublicID: "chat-llm", UpstreamID: "meta/llama-3.3-70b", DisplayName: "Chat LLM", Kind: modelcatalog.KindChat, Enabled: true, SupportsVision: true, SupportsTools: true, SupportsReasoning: true, ReasoningWireFormat: "chain_of_thought", CapabilityVerifiedAt: &verified, CreatedAt: verified, BlockedByKeyIDs: []int64{}},
		{ID: 2, PublicID: "embed-qa", UpstreamID: "nvidia/embed-qa-4", DisplayName: "Embedding QA", Kind: modelcatalog.KindEmbedding, BlockedByKeyIDs: []int64{7}},
	}
}

func contractCandidates() []modelcatalog.Candidate {
	return []modelcatalog.Candidate{
		{UpstreamID: "meta/llama-3.3-70b", DisplayName: "Chat LLM", Kind: modelcatalog.KindChat, SupportsVision: true, SupportsTools: true, SupportsReasoning: true, ReasoningWireFormat: "chain_of_thought"},
		{UpstreamID: "nvidia/embed-qa-4", DisplayName: "Embedding QA", Kind: modelcatalog.KindEmbedding},
	}
}

func contractAccessKeys() []accesskey.Key {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	lastUsed := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	expires := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return []accesskey.Key{
		{ID: 2, Name: "ci", Prefix: "nvr_ci", CreatedAt: now, LastUsedAt: &lastUsed, ExpiresAt: &expires, RPMLimit: 60, TPMLimit: 10000, MaxConcurrent: 4},
		{ID: 3, Name: "revoked", Prefix: "nvr_rev", CreatedAt: now, RevokedAt: &now},
	}
}

func contractNVIDIAKeys() []nvidiakey.Key {
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	cooldown := now.Add(2 * time.Hour)
	return []nvidiakey.Key{
		{ID: 7, DisplayPrefix: "nvapi-a1", DisplaySuffix: "b2c3", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: 8, DisplayPrefix: "nvapi-d4", DisplaySuffix: "e5f6", Enabled: false, AuthInvalid: true, CooldownUntil: &cooldown, CooldownReason: "rate_limited", CooldownLevel: 2, ConsecutiveFailures: 3, LastErrorAt: &now, LastErrorCode: "upstream_rate_limited", CreatedAt: now, UpdatedAt: now},
	}
}

func contractMonitoringSnapshot() observability.MonitoringSnapshot {
	from := time.Date(2026, 8, 1, 5, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	firstTokenP50 := int64(120)
	firstTokenP95 := int64(840)
	return observability.MonitoringSnapshot{
		Range: observability.MonitoringRange24Hours,
		From:  from, To: to,
		Summary: observability.MonitoringSummary{
			RequestCount: 100, SuccessCount: 95, FailureCount: 5, SuccessRate: 95,
			AverageDurationMS: 900, AverageFirstByteMS: 700, AverageFirstTokenMS: 210,
			AverageQueueMS: 12, TotalAttempts: 110, PromptTokens: 1000, CompletionTokens: 500,
			FirstTokenP50MS: &firstTokenP50, FirstTokenP95MS: &firstTokenP95,
		},
		Series: []observability.MonitoringSeriesPoint{
			{Bucket: "2026-08-01T05:00:00Z", RequestCount: 40, SuccessCount: 38, FailureCount: 2, AverageDurationMS: 800, AverageFirstByteMS: 600, AverageFirstTokenMS: 180, AverageQueueMS: 10, TotalAttempts: 44, PromptTokens: 400, CompletionTokens: 200},
			{Bucket: "2026-08-01T06:00:00Z", RequestCount: 60, SuccessCount: 57, FailureCount: 3, AverageDurationMS: 950, AverageFirstByteMS: 750, AverageFirstTokenMS: 230, AverageQueueMS: 14, TotalAttempts: 66, PromptTokens: 600, CompletionTokens: 300},
		},
	}
}

func contractMonitoringLogs() observability.RequestLogsPage {
	firstToken := int64(120)
	modelID := "chat-llm"
	return observability.RequestLogsPage{
		Items: []observability.RequestLog{
			{RequestID: "req-safe", Endpoint: "/v1/chat/completions", ModelID: &modelID, HTTPStatus: 200, Outcome: observability.OutcomeSuccess, IsStream: true, QueueMS: 13, FirstTokenMS: &firstToken, DurationMS: 921, AttemptCount: 1, PromptTokens: contractInt64Pointer(100), CompletionTokens: contractInt64Pointer(20), CreatedAt: "2026-08-02T07:00:00.000Z"},
			{RequestID: "req-failed", Endpoint: "/v1/responses", HTTPStatus: 502, Outcome: observability.OutcomeFailure, DurationMS: 40, AttemptCount: 2, CreatedAt: "2026-08-02T07:05:00.000Z"},
		},
		Page: 1, PageSize: 50, Total: 2, HasMore: false,
	}
}

func timePointer(value time.Time) *time.Time { return &value }

func contractInt64Pointer(value int64) *int64 { return &value }
