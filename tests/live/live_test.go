//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveNVIDIA(t *testing.T) {
	config, reason := loadConfig()
	if reason != "" {
		t.Logf("case=Configuration status=SKIP duration=0s reason=%s", reason)
		t.Skipf("SKIP: %s", reason)
	}
	client := newLiveClient(config)
	var enabledModels map[string]struct{}

	runLiveCase(t, "Models", func(t *testing.T) {
		enabledModels = testModels(t, client)
	})
	runLiveCase(t, "ChatNonstream", func(t *testing.T) {
		requireEnabledModel(t, enabledModels, config.chatModel)
		testChat(t, client, config.chatModel, false)
	})
	runLiveCase(t, "ChatStream", func(t *testing.T) {
		requireEnabledModel(t, enabledModels, config.chatModel)
		testChat(t, client, config.chatModel, true)
	})
	runLiveCase(t, "ResponsesNonstream", func(t *testing.T) {
		requireEnabledModel(t, enabledModels, config.responsesModel)
		testResponses(t, client, config.responsesModel, false)
	})
	runLiveCase(t, "ResponsesStream", func(t *testing.T) {
		requireEnabledModel(t, enabledModels, config.responsesModel)
		testResponses(t, client, config.responsesModel, true)
	})
	runLiveCase(t, "Embedding", func(t *testing.T) {
		if config.embeddingModel == "" {
			t.Skip("SKIP: NVIDIA_ROUTER_LIVE_EMBEDDING_MODEL is not configured; Embedding is required for acceptance")
		}
		requireEnabledModel(t, enabledModels, config.embeddingModel)
		testEmbedding(t, client, config.embeddingModel)
	})
	runLiveCase(t, "ASR", func(t *testing.T) {
		if config.asrModel == "" || config.asrFile == "" {
			t.Skip("SKIP: ASR model and non-empty audio file are not configured")
		}
		if strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_ASR_CAPABILITY_VERIFIED_AT")) == "" {
			t.Skip("SKIP: ASR capability_verified_at is absent; real capability verification is required")
		}
		requireEnabledModel(t, enabledModels, config.asrModel)
		testASR(t, client, config.asrModel, config.asrFile)
	})
	runLiveCase(t, "TTS", func(t *testing.T) {
		if config.ttsModel == "" {
			t.Skip("SKIP: TTS model is not configured")
		}
		if strings.TrimSpace(os.Getenv("NVIDIA_ROUTER_LIVE_TTS_CAPABILITY_VERIFIED_AT")) == "" {
			t.Skip("SKIP: TTS capability_verified_at is absent; real capability verification is required")
		}
		requireEnabledModel(t, enabledModels, config.ttsModel)
		testTTS(t, client, config.ttsModel, config.ttsVoice)
	})
}

func testModels(t *testing.T, client *liveClient) map[string]struct{} {
	response := client.request(t, http.MethodGet, "/v1/models", nil, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failNow(t)
	}
	var payload struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if !decodeJSON(response, &payload) || payload.Object != "list" || len(payload.Data) == 0 {
		failNow(t)
	}
	models := make(map[string]struct{}, len(payload.Data))
	for _, model := range payload.Data {
		if strings.TrimSpace(model.ID) == "" {
			failNow(t)
		}
		models[model.ID] = struct{}{}
	}
	return models
}

func testChat(t *testing.T, client *liveClient, model string, stream bool) {
	payload := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "Reply with exactly: ok"}},
		"stream":   stream,
	}
	response := client.postJSON(t, "/v1/chat/completions", payload)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failNow(t)
	}
	if stream {
		if !validChatStream(response) {
			failNow(t)
		}
		return
	}
	var result struct {
		Choices []json.RawMessage `json:"choices"`
	}
	if !decodeJSON(response, &result) || len(result.Choices) == 0 {
		failNow(t)
	}
}

func testResponses(t *testing.T, client *liveClient, model string, stream bool) {
	payload := map[string]any{
		"model":  model,
		"input":  "Reply with exactly: ok",
		"stream": stream,
	}
	response := client.postJSON(t, "/v1/responses", payload)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failNow(t)
	}
	if stream {
		if !validResponsesStream(response) {
			failNow(t)
		}
		return
	}
	var result struct {
		Object string            `json:"object"`
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
	}
	if !decodeJSON(response, &result) || result.Object != "response" || result.Status != "completed" || len(result.Output) == 0 {
		failNow(t)
	}
}

func testEmbedding(t *testing.T, client *liveClient, model string) {
	payload := map[string]any{"model": model, "input": "live routing verification"}
	response := client.postJSON(t, "/v1/embeddings", payload)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failNow(t)
	}
	var result struct {
		Object string `json:"object"`
		Data   []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if !decodeJSON(response, &result) || result.Object != "list" || len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		failNow(t)
	}
}

func testASR(t *testing.T, client *liveClient, model, filename string) {
	audio, err := os.ReadFile(filename)
	if err != nil || len(audio) == 0 {
		failNow(t)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if writer.WriteField("model", model) != nil {
		failNow(t)
	}
	partHeader, ok := audioPartHeader(filepath.Base(filename), filename)
	if !ok {
		failNow(t)
	}
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		failNow(t)
	}
	if _, err := part.Write(audio); err != nil || writer.Close() != nil {
		failNow(t)
	}
	response := client.request(t, http.MethodPost, "/v1/audio/transcriptions", &body, writer.FormDataContentType())
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failNow(t)
	}
	var result struct {
		Text       string `json:"text"`
		Transcript string `json:"transcript"`
	}
	if !decodeJSON(response, &result) || (strings.TrimSpace(result.Text) == "" && strings.TrimSpace(result.Transcript) == "") {
		failNow(t)
	}
}

func audioPartHeader(filename, path string) (textproto.MIMEHeader, bool) {
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if !strings.HasPrefix(contentType, "audio/") {
		return nil, false
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)
	return header, true
}

func testTTS(t *testing.T, client *liveClient, model, voice string) {
	payload := map[string]any{
		"model": model, "input": "NVIDIA router live verification.", "voice": voice,
	}
	response := client.postJSON(t, "/v1/audio/speech", payload)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !validAudioResponse(response) {
		failNow(t)
	}
}

func requireEnabledModel(t *testing.T, models map[string]struct{}, model string) {
	t.Helper()
	if _, ok := models[model]; !ok {
		failNow(t)
	}
}
