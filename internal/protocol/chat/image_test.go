package chat

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"nvidia-router/internal/modelcatalog"
)

func TestParseAcceptsHTTPSImageWithoutFetchingIt(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	request, err := Parse(imagePayload(server.URL + "/private.png"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := request.Requirements(); !got.Vision {
		t.Fatalf("requirements = %+v, want Vision", got)
	}
	if requests.Load() != 0 {
		t.Fatalf("image validation made %d remote requests", requests.Load())
	}
	body, err := request.MarshalFor(visionModel())
	if err != nil {
		t.Fatalf("MarshalFor: %v", err)
	}
	if !strings.Contains(string(body), server.URL+"/private.png") {
		t.Fatal("prepared body did not preserve HTTPS image URL")
	}
}

func TestParseAcceptsHTTPSImageWithCaseInsensitiveScheme(t *testing.T) {
	request, err := Parse(imagePayload("HTTPS://example.com/private.png"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !request.Requirements().Vision {
		t.Fatalf("requirements = %+v, want Vision", request.Requirements())
	}
}

func TestParseRejectsNonHTTPSImageURLs(t *testing.T) {
	for _, source := range []string{
		"http://example.com/image.png",
		"file:///etc/passwd",
		"ftp://example.com/image.png",
		"https:///missing-host.png",
		"https://user@example.com/image.png",
	} {
		t.Run(source, func(t *testing.T) {
			_, err := Parse(imagePayload(source))
			_ = requireRequestError(t, err, "invalid_image_url", "messages[0].content[0].image_url.url")
		})
	}
}

func TestParseAcceptsSupportedStrictBase64Images(t *testing.T) {
	for _, mediaType := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		t.Run(mediaType, func(t *testing.T) {
			source := "data:" + mediaType + ";base64,aGVsbG8="
			request, err := Parse(imagePayload(source))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !request.Requirements().Vision {
				t.Fatal("data image did not require vision")
			}
		})
	}
}

func TestParseRejectsInvalidDataImages(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "unsupported MIME", source: "data:image/svg+xml;base64,PHN2Zz4="},
		{name: "missing base64 marker", source: "data:image/png,hello"},
		{name: "missing padding", source: "data:image/png;base64,aGVsbG8"},
		{name: "non alphabet byte", source: "data:image/png;base64,aGVs*bG8="},
		{name: "newline", source: "data:image/png;base64,aGVs\nbG8="},
		{name: "empty", source: "data:image/png;base64,"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(imagePayload(tt.source))
			_ = requireRequestError(t, err, "invalid_image_data", "messages[0].content[0].image_url.url")
		})
	}
}

func TestParseEnforcesDecodedImageLimit(t *testing.T) {
	t.Run("exact limit", func(t *testing.T) {
		payload := imagePayload(dataImageURL(maxDecodedImageBytes))
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		request, err := Parse(payload)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatalf("Parse exact limit: %v", err)
		}
		if !request.Requirements().Vision {
			t.Fatal("maximum data image did not require vision")
		}
		if allocated := after.TotalAlloc - before.TotalAlloc; allocated > uint64(len(payload))*4 {
			t.Fatalf("Parse allocated %d bytes for %d-byte request", allocated, len(payload))
		}
	})

	t.Run("over limit", func(t *testing.T) {
		_, err := Parse(imagePayload(dataImageURL(maxDecodedImageBytes + 1)))
		_ = requireRequestError(t, err, "image_too_large", "messages[0].content[0].image_url.url")
	})
}

func TestParseEnforcesTotalJSONLimit(t *testing.T) {
	prefix := []byte(`{"model":"public-model","messages":[{"role":"user","content":"x"}],"padding":"`)
	suffix := []byte(`"}`)
	payload := make([]byte, 0, MaxRequestBytes+1)
	payload = append(payload, prefix...)
	payload = append(payload, strings.Repeat("a", MaxRequestBytes+1-len(prefix)-len(suffix))...)
	payload = append(payload, suffix...)
	if len(payload) != MaxRequestBytes+1 {
		t.Fatalf("test payload length = %d", len(payload))
	}

	_, err := Parse(payload)
	_ = requireRequestError(t, err, "request_too_large", "body")
}

func TestMarshalForwardsImageToModelWithoutLocalVisionHint(t *testing.T) {
	request, err := Parse(imagePayload("https://example.com/image.png"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	body, err := request.MarshalFor(chatModel())
	if err != nil {
		t.Fatalf("MarshalFor: %v", err)
	}
	if !strings.Contains(string(body), "https://example.com/image.png") {
		t.Fatal("image URL was not forwarded")
	}
}

func TestParseValidatesImageContentShape(t *testing.T) {
	tests := []struct {
		name    string
		content string
		param   string
	}{
		{name: "content object", content: `{}`, param: "messages[0].content"},
		{name: "part scalar", content: `[7]`, param: "messages[0].content[0]"},
		{name: "part type", content: `[{"type":7}]`, param: "messages[0].content[0].type"},
		{name: "image URL object", content: `[{"type":"image_url","image_url":"https://example.com/x.png"}]`, param: "messages[0].content[0].image_url"},
		{name: "image URL string", content: `[{"type":"image_url","image_url":{"url":7}}]`, param: "messages[0].content[0].image_url.url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(`{"model":"public-model","messages":[{"role":"user","content":` + tt.content + `}]}`)
			_, err := Parse(payload)
			_ = requireRequestError(t, err, "invalid_parameter", tt.param)
		})
	}
}

func TestParseRejectsKnownStringWhoseLastDuplicateIsNull(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		param   string
	}{
		{
			name:    "role",
			payload: `{"model":"public-model","messages":[{"role":"user","role":null,"content":"x"}]}`,
			param:   "messages[0].role",
		},
		{
			name: "part type",
			payload: `{"model":"public-model","messages":[{"role":"user","content":[` +
				`{"type":"image_url","type":null,"image_url":{"url":"https://example.com/x.png"}}]}]}`,
			param: "messages[0].content[0].type",
		},
		{
			name: "image URL",
			payload: `{"model":"public-model","messages":[{"role":"user","content":[` +
				`{"type":"image_url","image_url":{"url":"https://example.com/x.png","url":null}}]}]}`,
			param: "messages[0].content[0].image_url.url",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.payload))
			_ = requireRequestError(t, err, "invalid_parameter", tt.param)
		})
	}
}

func TestParseRejectsAmbiguousKnownMessageKeys(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		code    string
		param   string
	}{
		{
			name: "role case", payload: `{"model":"public-model","messages":[{"ROLE":"user","content":"x"}]}`,
			code: "missing_required_parameter", param: "messages[0].role",
		},
		{
			name: "role duplicate", payload: `{"model":"public-model","messages":[{"role":null,"role":"user","content":"x"}]}`,
			code: "invalid_parameter", param: "messages[0].role",
		},
		{
			name: "part type case",
			payload: `{"model":"public-model","messages":[{"role":"user","content":[` +
				`{"TYPE":"image_url","image_url":{"url":"https://example.com/x.png"}}]}]}`,
			code: "missing_required_parameter", param: "messages[0].content[0].type",
		},
		{
			name: "part type duplicate",
			payload: `{"model":"public-model","messages":[{"role":"user","content":[` +
				`{"type":null,"type":"image_url","image_url":{"url":"https://example.com/x.png"}}]}]}`,
			code: "invalid_parameter", param: "messages[0].content[0].type",
		},
		{
			name: "URL case",
			payload: `{"model":"public-model","messages":[{"role":"user","content":[` +
				`{"type":"image_url","image_url":{"URL":"https://example.com/x.png"}}]}]}`,
			code: "missing_required_parameter", param: "messages[0].content[0].image_url.url",
		},
		{
			name: "URL duplicate",
			payload: `{"model":"public-model","messages":[{"role":"user","content":[` +
				`{"type":"image_url","image_url":{"url":null,"url":"https://example.com/x.png"}}]}]}`,
			code: "invalid_parameter", param: "messages[0].content[0].image_url.url",
		},
		{
			name: "content case",
			payload: `{"model":"public-model","messages":[{"role":"user",` +
				`"CONTENT":[{"type":"image_url","image_url":{"url":"https://example.com/x.png"}}]}]}`,
			code: "invalid_parameter", param: "messages[0].content",
		},
		{
			name: "tool calls case",
			payload: `{"model":"public-model","messages":[{"role":"assistant","content":null,` +
				`"Tool_Calls":[{"type":"function","function":{"name":"lookup"}}]}]}`,
			code: "invalid_parameter", param: "messages[0].tool_calls",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.payload))
			_ = requireRequestError(t, err, tt.code, tt.param)
		})
	}
}

func TestParseHandlesEscapedJSONDuringStrictTraversal(t *testing.T) {
	payloads := []string{
		`{"model":"public-model","messages":[{"\u0072ole":"user","content":"x"}]}`,
		`{"model":"public-model","messages":[{"role":"user","content":[` +
			`{"type":"image_url","image_url":{"url":"https:\/\/example.com\/x.png"}}]}]}`,
		`{"model":"public-model","messages":[{"role":"user","content":"x",` +
			`"future":{"text":"comma,]}\"\\end","nested":[{"value":"x,y"}]}}]}`,
	}
	for _, payload := range payloads {
		if _, err := Parse([]byte(payload)); err != nil {
			t.Fatalf("Parse(%s): %v", payload, err)
		}
	}
}

func TestParseRejectsEscapedDuplicateKnownKey(t *testing.T) {
	payload := []byte(`{"model":"public-model","messages":[{"\u0072ole":"user","role":"assistant","content":"x"}]}`)
	_, err := Parse(payload)
	_ = requireRequestError(t, err, "invalid_parameter", "messages[0].role")
}

func imagePayload(source string) []byte {
	encodedSource, _ := json.Marshal(source)
	payload := make([]byte, 0, len(encodedSource)+128)
	payload = append(payload, `{"model":"public-model","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":`...)
	payload = append(payload, encodedSource...)
	payload = append(payload, `}}]}]}`...)
	return payload
}

func dataImageURL(decodedBytes int) string {
	var encoded strings.Builder
	encoded.Grow(len("data:image/png;base64,") + base64.StdEncoding.EncodedLen(decodedBytes))
	encoded.WriteString("data:image/png;base64,")
	encoder := base64.NewEncoder(base64.StdEncoding, &encoded)
	zeroes := make([]byte, 32<<10)
	for remaining := decodedBytes; remaining > 0; {
		chunk := min(remaining, len(zeroes))
		_, _ = encoder.Write(zeroes[:chunk])
		remaining -= chunk
	}
	_ = encoder.Close()
	return encoded.String()
}

func visionModel() modelcatalog.Model {
	model := chatModel()
	model.SupportsVision = true
	return model
}
