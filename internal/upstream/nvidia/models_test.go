package nvidia

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"nvidia-router/tests/mocknvidia"
)

func TestModelsSupportsKnownResponseShapes(t *testing.T) {
	responses := []string{
		"[{\"id\":\"model-b\"},{\"id\":\"model-a\"},{\"id\":\"model-a\"}]",
		"{\"data\":[{\"id\":\"model-b\"},{\"id\":\"model-a\"}]}",
		"{\"models\":[{\"id\":\"model-b\"},{\"id\":\"model-a\"}]}",
		"{\"results\":[{\"id\":\"model-b\"},{\"id\":\"model-a\"}]}",
	}
	for _, body := range responses {
		server := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: body})
		t.Cleanup(server.Close)
		client := newTestClient(t, server.URL(), server.Client())

		models, err := client.Models(context.Background(), "test-token")
		if err != nil {
			t.Fatalf("Models: %v", err)
		}
		if strings.Join(models, ",") != "model-a,model-b" {
			t.Fatalf("models = %v", models)
		}
		request := server.Requests()[0]
		if request.Method != http.MethodGet || request.Path != "/v1/models" {
			t.Fatalf("request = %s %s", request.Method, request.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
	}
}

func TestModelsRejectsEmptyAndMalformedResponsesWithoutLeakingBody(t *testing.T) {
	for _, body := range []string{"", "{\"data\":", "{\"data\":[]}", "{\"other\":[]}", "{\"data\":[{\"id\":\"\"}]}"} {
		t.Run(body, func(t *testing.T) {
			server := mocknvidia.New(mocknvidia.Script{Status: http.StatusOK, Body: body})
			t.Cleanup(server.Close)
			_, err := newTestClient(t, server.URL(), server.Client()).Models(context.Background(), "test-token")
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("Models error = %v, want ErrProtocol", err)
			}
			if body != "" && strings.Contains(err.Error(), body) {
				t.Fatal("protocol error contains upstream body")
			}
		})
	}
}

func TestValidationClassifiesResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   ValidationState
	}{
		{"valid", 200, "{\"data\":[{\"id\":\"model-a\"}]}", ValidationValid},
		{"unauthorized", 401, "{\"error\":\"credential-secret\"}", ValidationInvalidCredential},
		{"forbidden", 403, "{}", ValidationInvalidCredential},
		{"rate limited", 429, "{}", ValidationTemporarilyUnavailable},
		{"internal", 500, "{}", ValidationTemporarilyUnavailable},
		{"bad gateway", 502, "{}", ValidationTemporarilyUnavailable},
		{"unavailable", 503, "{}", ValidationTemporarilyUnavailable},
		{"timeout", 504, "{}", ValidationTemporarilyUnavailable},
		{"malformed success", 200, "{\"data\":", ValidationIndeterminate},
		{"unexpected", 418, "{}", ValidationIndeterminate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := mocknvidia.New(mocknvidia.Script{
				Status:  test.status,
				Body:    test.body,
				Headers: http.Header{"X-Request-Id": []string{"request-123"}, "X-Secret": []string{"hidden"}},
			})
			t.Cleanup(server.Close)
			result := newTestClient(t, server.URL(), server.Client()).ValidateCredential(context.Background(), "test-token")
			if result.State != test.want {
				t.Fatalf("state = %v, want %v", result.State, test.want)
			}
			if result.RequestID != "request-123" {
				t.Fatalf("request ID = %q", result.RequestID)
			}
			if result.State == ValidationValid && strings.Join(result.Models, ",") != "model-a" {
				t.Fatalf("valid models = %v", result.Models)
			}
			if strings.Contains(result.SafeError, "credential-secret") || strings.Contains(result.SafeError, "hidden") {
				t.Fatal("validation result exposes upstream body or non-allowlisted header")
			}
		})
	}
}

func TestValidationTreatsNetworkAndTimeoutAsTemporary(t *testing.T) {
	client := newTestClient(t, "https://example.invalid", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})})
	if result := client.ValidateCredential(context.Background(), "test-token"); result.State != ValidationTemporarilyUnavailable {
		t.Fatalf("network state = %v", result.State)
	}

	server := mocknvidia.New(mocknvidia.Script{Status: 200, Body: "{\"data\":[]}", Delay: time.Second})
	t.Cleanup(server.Close)
	timeoutClient := server.Client()
	timeoutClient.Timeout = 10 * time.Millisecond
	if result := newTestClient(t, server.URL(), timeoutClient).ValidateCredential(context.Background(), "test-token"); result.State != ValidationTemporarilyUnavailable {
		t.Fatalf("timeout state = %v", result.State)
	}
}

func TestValidationReadsAtMostEightKiBOfErrorBody(t *testing.T) {
	body := &countingReadCloser{reader: strings.NewReader(strings.Repeat("x", 32<<10))}
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Header: make(http.Header), Body: body}, nil
	})}
	result := newTestClient(t, "https://example.invalid", httpClient).ValidateCredential(context.Background(), "test-token")
	if result.State != ValidationTemporarilyUnavailable {
		t.Fatalf("state = %v", result.State)
	}
	if body.read > 8<<10 {
		t.Fatalf("error body bytes read = %d, want <= %d", body.read, 8<<10)
	}
}

func TestMockServerQueuesResponsesAndDetectsStreamCancellation(t *testing.T) {
	server := mocknvidia.New(
		mocknvidia.Script{Status: 200, Body: "{\"ok\":true}"},
		mocknvidia.Script{Status: 200, SSE: []mocknvidia.SSEChunk{{Data: "data: first\n\n"}, {Data: "data: second\n\n", Delay: time.Second}}},
	)
	t.Cleanup(server.Close)

	request, _ := http.NewRequest(http.MethodGet, server.URL()+"/first", nil)
	request.Header.Set("X-Test", "captured")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	response.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request, _ = http.NewRequestWithContext(ctx, http.MethodGet, server.URL()+"/stream", nil)
	response, err = server.Client().Do(request)
	if err == nil {
		_, _ = io.ReadAll(response.Body)
		response.Body.Close()
	}
	deadline := time.Now().Add(time.Second)
	for server.CanceledCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if server.Count() != 2 || server.CanceledCount() != 1 {
		t.Fatalf("count/canceled = %d/%d", server.Count(), server.CanceledCount())
	}
	if got := server.Requests()[0].Header.Get("X-Test"); got != "captured" {
		t.Fatalf("captured header = %q", got)
	}
}

func newTestClient(t *testing.T, baseURL string, httpClient *http.Client) *Client {
	t.Helper()
	descriptor := DefaultDescriptor()
	descriptor.Models.URL = baseURL + "/v1/models"
	client, err := NewClient(httpClient, descriptor)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type countingReadCloser struct {
	reader io.Reader
	read   int
}

func (r *countingReadCloser) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	r.read += count
	return count, err
}

func (*countingReadCloser) Close() error { return nil }
