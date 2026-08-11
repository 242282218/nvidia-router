package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"nvidia-router/internal/accesskey"
	"nvidia-router/internal/clock"
	"nvidia-router/internal/config"
	"nvidia-router/internal/crypto"
	"nvidia-router/internal/keystate"
	"nvidia-router/internal/router"
)

func TestSettingsPatchAppliesToSharedRuntimeStoreAndNewAcquires(t *testing.T) {
	db := openAppDatabase(t)
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{InitialAdminPassword: testInitialAdminPassword, DataDir: t.TempDir(), TempDir: t.TempDir(), MasterKey: [32]byte{1}},
		DB:     db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: clock.RealClock{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(func() {
		server.Close()
		_ = app.Close()
	})
	if _, err := db.Exec("UPDATE admins SET must_change_password = 0 WHERE id = 1"); err != nil {
		t.Fatalf("enable management session: %v", err)
	}
	login := authRequest(t, server.Client(), http.MethodPost, server.URL+"/admin/api/auth/login", `{"username":"admin","password":"test-initial-admin-password"}`, nil, server.URL)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d: %s", login.StatusCode, readResponse(t, login))
	}
	session := responseSessionCookie(t, login)

	patch := authRequest(t, server.Client(), http.MethodPatch, server.URL+"/admin/api/settings", `{"queue_capacity":1,"connect_timeout_ms":2500,"first_byte_timeout_ms":3500,"nonstream_total_timeout_ms":4500}`, session, server.URL)
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200: %s", patch.StatusCode, readResponse(t, patch))
	}
	_ = patch.Body.Close()
	snapshot := app.RuntimeSettings.Snapshot()
	if snapshot.QueueCapacity != 1 || snapshot.ConnectTimeoutMS != 2500 || snapshot.FirstByteTimeoutMS != 3500 || snapshot.NonstreamTotalTimeoutMS != 4500 {
		t.Fatalf("runtime snapshot = %#v", snapshot)
	}
	var queueCapacity, connectTimeout int
	if err := db.QueryRow("SELECT queue_capacity, connect_timeout_ms FROM runtime_settings WHERE id = 1").Scan(&queueCapacity, &connectTimeout); err != nil {
		t.Fatalf("load persisted settings: %v", err)
	}
	if queueCapacity != 1 || connectTimeout != 2500 {
		t.Fatalf("persisted queue/connect = %d/%d", queueCapacity, connectTimeout)
	}

	app.Pool.LoadSnapshot([]keystate.KeySnapshot{{ID: 1, Enabled: true}}, nil)
	lease, err := app.Pool.Acquire(context.Background(), 1, nil, false)
	if err != nil {
		t.Fatalf("acquire active lease: %v", err)
	}
	defer lease.Release()
	waitCtx, cancelWait := context.WithCancel(context.Background())
	defer cancelWait()
	waitResult := make(chan error, 1)
	go func() {
		waitingLease, acquireErr := app.Pool.Acquire(waitCtx, 1, nil, false)
		if waitingLease != nil {
			waitingLease.Release()
		}
		waitResult <- acquireErr
	}()
	waitForQueueLength(t, app, 1)
	_, err = app.Pool.Acquire(context.Background(), 1, nil, false)
	if err == nil || !strings.Contains(err.Error(), "queue") {
		t.Fatalf("second queued acquire error = %v, want queue full", err)
	}
	cancelWait()
	if waitErr := <-waitResult; !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("waiting acquire error = %v, want context canceled", waitErr)
	}
}

func TestSettingsPatchKeepsInFlightSnapshotAndAppliesToNewNVIDIAAttempt(t *testing.T) {
	transport := newRuntimeBudgetTransport(t)
	app, accessToken := newRuntimeBudgetTestApp(t, transport.transport)
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	session := loginRuntimeAdmin(t, server)
	before := app.RuntimeSettings.Snapshot()
	if app.Server.settings != app.RuntimeSettings {
		t.Fatal("server and application do not share the runtime settings store")
	}

	firstDone := make(chan chatResponse, 1)
	go func() {
		firstDone <- sendRuntimeChat(server.Client(), server.URL, accessToken)
	}()
	first := receiveRuntimeBudget(t, transport.started)
	if first.ConnectTimeout != time.Duration(before.ConnectTimeoutMS)*time.Millisecond {
		t.Fatalf("first connect timeout = %v, want %dms", first.ConnectTimeout, before.ConnectTimeoutMS)
	}

	patch := authRequest(t, server.Client(), http.MethodPatch, server.URL+"/admin/api/settings", `{"connect_timeout_ms":2500,"first_byte_timeout_ms":3500,"nonstream_total_timeout_ms":4500,"shutdown_grace_ms":5500}`, session, server.URL)
	if patch.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200: %s", patch.StatusCode, readResponse(t, patch))
	}
	_ = patch.Body.Close()
	if grace := app.Server.shutdownGrace(); grace != 5500*time.Millisecond {
		t.Fatalf("server shutdown grace = %v, want 5.5s", grace)
	}

	close(transport.continueFirst)
	firstAfterPatch := receiveRuntimeBudget(t, transport.afterPatch)
	if firstAfterPatch.ConnectTimeout != first.ConnectTimeout ||
		!firstAfterPatch.FirstByteDeadline.Equal(first.FirstByteDeadline) ||
		!firstAfterPatch.TotalDeadline.Equal(first.TotalDeadline) {
		t.Fatalf("in-flight budget changed after PATCH: before=%#v after=%#v", first, firstAfterPatch)
	}
	assertChatResponse(t, receiveChatResponse(t, firstDone))

	secondDone := make(chan chatResponse, 1)
	go func() {
		secondDone <- sendRuntimeChat(server.Client(), server.URL, accessToken)
	}()
	second := receiveRuntimeBudget(t, transport.started)
	assertDurationNear(t, second.ConnectTimeout, 2500*time.Millisecond)
	assertDurationNear(t, second.FirstByteDeadline.Sub(second.CapturedAt), 3500*time.Millisecond)
	assertDurationNear(t, second.TotalDeadline.Sub(second.CapturedAt), 4500*time.Millisecond)
	assertChatResponse(t, receiveChatResponse(t, secondDone))
}

func TestRuntimeSummaryEndpointUsesPoolSnapshot(t *testing.T) {
	db := openAppDatabase(t)
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{InitialAdminPassword: testInitialAdminPassword, DataDir: t.TempDir(), TempDir: t.TempDir(), MasterKey: [32]byte{1}},
		DB:     db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: clock.RealClock{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(func() {
		server.Close()
		_ = app.Close()
	})
	if _, err := db.Exec("UPDATE admins SET must_change_password = 0 WHERE id = 1"); err != nil {
		t.Fatalf("enable management session: %v", err)
	}
	login := authRequest(t, server.Client(), http.MethodPost, server.URL+"/admin/api/auth/login", `{"username":"admin","password":"test-initial-admin-password"}`, nil, server.URL)
	session := responseSessionCookie(t, login)
	app.Pool.LoadSnapshot([]keystate.KeySnapshot{{ID: 1, Enabled: true}, {ID: 2, Enabled: false}}, nil)
	lease, err := app.Pool.Acquire(context.Background(), 1, nil, false)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release()

	response := authRequest(t, server.Client(), http.MethodGet, server.URL+"/admin/api/runtime/summary", "", session, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("summary status = %d: %s", response.StatusCode, readResponse(t, response))
	}
	defer func() { _ = response.Body.Close() }()
	var body struct {
		Data struct {
			Active int `json:"active"`
			Keys   struct {
				Total    int `json:"total"`
				Disabled int `json:"disabled"`
			} `json:"keys"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if body.Data.Active != 1 || body.Data.Keys.Total != 2 || body.Data.Keys.Disabled != 1 {
		t.Fatalf("summary = %#v", body.Data)
	}
}

func waitForQueueLength(t *testing.T, app *App, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if app.Pool.Summary().Queue.Length == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("queue length = %d, want %d", app.Pool.Summary().Queue.Length, want)
}

type runtimeBudgetCapture struct {
	CapturedAt        time.Time
	ConnectTimeout    time.Duration
	FirstByteDeadline time.Time
	TotalDeadline     time.Time
}

type runtimeBudgetTransport struct {
	transport     *http.Transport
	started       chan runtimeBudgetCapture
	continueFirst chan struct{}
	afterPatch    chan runtimeBudgetCapture
	mu            sync.Mutex
	firstContext  context.Context
	calls         int
}

func newRuntimeBudgetTransport(t *testing.T) *runtimeBudgetTransport {
	t.Helper()
	capture := &runtimeBudgetTransport{
		started:       make(chan runtimeBudgetCapture, 2),
		continueFirst: make(chan struct{}),
		afterPatch:    make(chan runtimeBudgetCapture, 1),
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		capture.mu.Lock()
		capture.calls++
		firstCall := capture.calls == 1
		first := capture.firstContext
		capture.mu.Unlock()
		if firstCall {
			<-capture.continueFirst
			if first != nil {
				capture.afterPatch <- captureRuntimeBudgetContext(first)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{}]}`)
	}))
	t.Cleanup(upstream.Close)
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	base.DisableKeepAlives = true
	base.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		capture.started <- captureRuntimeBudgetContext(ctx)
		capture.mu.Lock()
		if capture.firstContext == nil {
			capture.firstContext = ctx
		}
		capture.mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	capture.transport = base
	return capture
}

func captureRuntimeBudgetContext(ctx context.Context) runtimeBudgetCapture {
	budget, _ := router.BudgetFromContext(ctx)
	totalDeadline := budget.TotalDeadline()
	if totalDeadline.IsZero() {
		totalDeadline, _ = ctx.Deadline()
	}
	return runtimeBudgetCapture{
		CapturedAt:        time.Now(),
		ConnectTimeout:    budget.ConnectTimeout(),
		FirstByteDeadline: budget.FirstByteDeadline(),
		TotalDeadline:     totalDeadline,
	}
}

type chatResponse struct {
	status int
	body   string
	err    error
}

func sendRuntimeChat(client *http.Client, baseURL, accessToken string) chatResponse {
	request, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/v1/chat/completions",
		strings.NewReader(chatBody("public-model")),
	)
	if err != nil {
		return chatResponse{err: err}
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := client.Do(request)
	if err != nil {
		return chatResponse{err: err}
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	return chatResponse{status: response.StatusCode, body: string(body), err: err}
}

func newRuntimeBudgetTestApp(t *testing.T, transport *http.Transport) (*App, string) {
	t.Helper()
	db := openAppDatabase(t)
	masterKey := [32]byte{1}
	keySet, err := crypto.New(masterKey)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	createdAccessKey, err := accesskey.NewService(accesskey.NewRepository(db), keySet, clock.RealClock{}).Create(context.Background(), "runtime")
	if err != nil {
		t.Fatalf("create access key: %v", err)
	}
	seedNVIDIAKeys(t, db, keySet, []string{"runtime-upstream-key"})
	seedChatModel(t, db)
	baseURL, err := url.Parse("http://nvidia.test")
	if err != nil {
		t.Fatalf("parse NVIDIA base URL: %v", err)
	}
	app, err := New(context.Background(), Dependencies{
		Config: config.Config{InitialAdminPassword: testInitialAdminPassword, DataDir: t.TempDir(), TempDir: t.TempDir(), MasterKey: masterKey, NVIDIABaseURL: baseURL},
		DB:     db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: clock.RealClock{},
		NVIDIAHTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("New: %v", err)
	}
	completeInitialPasswordChange(t, db)
	t.Cleanup(func() { _ = app.Close() })
	return app, createdAccessKey.Plaintext
}

func loginRuntimeAdmin(t *testing.T, server *httptest.Server) *http.Cookie {
	t.Helper()
	response := authRequest(t, server.Client(), http.MethodPost, server.URL+"/admin/api/auth/login", `{"username":"admin","password":"test-initial-admin-password"}`, nil, server.URL)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d: %s", response.StatusCode, readResponse(t, response))
	}
	cookie := responseSessionCookie(t, response)
	_ = response.Body.Close()
	return cookie
}

func receiveRuntimeBudget(t *testing.T, captures <-chan runtimeBudgetCapture) runtimeBudgetCapture {
	t.Helper()
	select {
	case capture := <-captures:
		return capture
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for NVIDIA runtime budget")
		return runtimeBudgetCapture{}
	}
}

func receiveChatResponse(t *testing.T, responses <-chan chatResponse) chatResponse {
	t.Helper()
	select {
	case response := <-responses:
		return response
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for chat response")
		return chatResponse{}
	}
}

func assertChatResponse(t *testing.T, response chatResponse) {
	t.Helper()
	if response.err != nil {
		t.Fatalf("chat request: %v", response.err)
	}
	if response.status != http.StatusOK {
		t.Fatalf("chat status = %d, want 200: %s", response.status, response.body)
	}
}

func assertDurationNear(t *testing.T, got, want time.Duration) {
	t.Helper()
	delta := got - want
	if delta < 0 {
		delta = -delta
	}
	if delta > 750*time.Millisecond {
		t.Fatalf("duration = %v, want about %v", got, want)
	}
}
