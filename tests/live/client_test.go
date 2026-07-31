//go:build live

package live

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

const maxLiveResponseBytes = 16 << 20

type liveConfig struct {
	baseURL        string
	accessKey      string
	chatModel      string
	responsesModel string
	embeddingModel string
	asrModel       string
	asrFile        string
	ttsModel       string
	ttsVoice       string
}

type liveClient struct {
	baseURL   string
	accessKey string
	http      *http.Client
}

func loadConfig() (liveConfig, string) {
	config := liveConfig{
		baseURL:        strings.TrimRight(strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_BASE_URL")), "/"),
		accessKey:      strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_ACCESS_KEY")),
		chatModel:      strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_CHAT_MODEL")),
		responsesModel: strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_RESPONSES_MODEL")),
		embeddingModel: strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_EMBEDDING_MODEL")),
		asrModel:       strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_ASR_MODEL")),
		asrFile:        strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_ASR_FILE")),
		ttsModel:       strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_TTS_MODEL")),
		ttsVoice:       strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_TTS_VOICE")),
	}
	if config.responsesModel == "" {
		config.responsesModel = config.chatModel
	}
	if config.ttsVoice == "" {
		config.ttsVoice = "alloy"
	}
	missing := make([]string, 0, 3)
	if config.baseURL == "" {
		missing = append(missing, "NVIDIA_ROUTER_LIVE_BASE_URL")
	}
	if config.accessKey == "" {
		missing = append(missing, "NVIDIA_ROUTER_LIVE_ACCESS_KEY")
	}
	if config.chatModel == "" {
		missing = append(missing, "NVIDIA_ROUTER_LIVE_CHAT_MODEL")
	}
	if len(missing) > 0 {
		return liveConfig{}, "missing " + strings.Join(missing, ", ")
	}
	parsed, err := url.Parse(config.baseURL)
	if err != nil || parsed.User != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return liveConfig{}, "NVIDIA_ROUTER_LIVE_BASE_URL must be an http(s) URL without credentials"
	}
	return config, ""
}

func newLiveClient(config liveConfig) *liveClient {
	return &liveClient{
		baseURL: config.baseURL, accessKey: config.accessKey,
		http: &http.Client{Timeout: 2 * time.Minute},
	}
}

func runLiveCase(t *testing.T, name string, test func(*testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		started := time.Now()
		t.Cleanup(func() {
			status := "PASS"
			if t.Skipped() {
				status = "SKIP"
			} else if t.Failed() {
				status = "FAIL"
			}
			t.Logf("case=%s status=%s duration=%s", name, status, time.Since(started).Round(time.Millisecond))
		})
		test(t)
	})
}

func (c *liveClient) postJSON(t *testing.T, path string, payload any) *http.Response {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		failNow(t)
	}
	return c.request(t, http.MethodPost, path, bytes.NewReader(body), "application/json")
}

func (c *liveClient) request(t *testing.T, method, path string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, c.baseURL+path, body)
	if err != nil {
		failNow(t)
	}
	request.Header.Set("Authorization", "Bearer "+c.accessKey)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.http.Do(request)
	if err != nil {
		failNow(t)
	}
	return response
}

func decodeJSON(response *http.Response, target any) bool {
	body, ok := readLimited(response.Body)
	if !ok {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if decoder.Decode(target) != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func validChatStream(response *http.Response) bool {
	events, ok := readSSE(response)
	if !ok {
		return false
	}
	for _, event := range events {
		if strings.TrimSpace(event.data) == "[DONE]" {
			return true
		}
	}
	return false
}

func validResponsesStream(response *http.Response) bool {
	events, ok := readSSE(response)
	if !ok {
		return false
	}
	for _, event := range events {
		if event.name == "response.completed" {
			return true
		}
	}
	return false
}

type sseEvent struct {
	name string
	data string
}

func readSSE(response *http.Response) ([]sseEvent, bool) {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		return nil, false
	}
	body, ok := readLimited(response.Body)
	if !ok {
		return nil, false
	}
	return parseSSE(body)
}

func parseSSE(body []byte) ([]sseEvent, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	events := make([]sseEvent, 0, 8)
	current := sseEvent{}
	flush := func() {
		if current.name != "" || current.data != "" {
			events = append(events, current)
		}
		current = sseEvent{}
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event:") {
			current.name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			if current.data != "" {
				current.data += "\n"
			}
			current.data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	flush()
	return events, scanner.Err() == nil && len(events) > 0
}

func validAudioResponse(response *http.Response) bool {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || (!strings.HasPrefix(mediaType, "audio/") && mediaType != "application/octet-stream") {
		return false
	}
	body, ok := readLimited(response.Body)
	return ok && len(body) > 0
}

func readLimited(reader io.Reader) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(reader, maxLiveResponseBytes+1))
	return body, err == nil && len(body) <= maxLiveResponseBytes
}

func failNow(t *testing.T) {
	t.Helper()
	t.FailNow()
}
