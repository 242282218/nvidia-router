package observability

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"nvidia-router/internal/clock"
)

// usageCaptureLimit bounds the buffered copy of a non-streaming JSON body.
// The whole body is needed because the usage object may appear anywhere in it.
const usageCaptureLimit = 2 << 20

// usageTailCaptureLimit bounds the buffered tail of an SSE stream. Usage only
// ever arrives in a trailing event and lastSSEEventData scans backwards, so
// retaining the whole stream was pure overhead: a long generation held up to
// usageCaptureLimit of heap per in-flight request purely to read two integers,
// and any stream that exceeded that limit dropped its usage entirely. A bounded
// tail is both far cheaper and strictly more capable.
const usageTailCaptureLimit = 64 << 10

var usageCaptureEndpoints = map[string]struct{}{
	"/v1/chat/completions": {},
	"/v1/embeddings":       {},
	"/v1/responses":        {},
}

type RequestRecorder interface {
	Record(context.Context, RequestRecord) error
}

func HTTPMiddleware(recorder RequestRecorder, source clock.Clock, logger *slog.Logger, next http.Handler) http.Handler {
	if source == nil {
		source = clock.RealClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := source.Now()
		ctx, state := WithRequestState(request.Context())
		requestID := newRequestID()
		writer.Header().Set("X-Request-ID", requestID)
		tracked := newTrackingWriter(writer, ctx, started, source, request.URL.Path)
		next.ServeHTTP(tracked, request.WithContext(ctx))
		metadata := state.Snapshot()
		// Run the captured body through bearer-token redaction before usage
		// parsing as a defensive safety net: NVIDIA responses never carry
		// `Bearer <token>` text, so redaction is a no-op on legitimate content
		// (the fast path returns early when "bearer" is absent). If an
		// upstream error echo somehow leaked an Authorization-like body here,
		// the JSON parse would no-op usage (already the failure-shaped branch)
		// without persisting the token.
		prompt, completion := parseUsage([]byte(RedactBearerToken(string(tracked.body.Bytes()))), tracked.captureComplete, tracked.captureEnabled, tracked.status, tracked.Header().Get("Content-Type"))
		if metadata.PromptTokens == nil {
			metadata.PromptTokens = prompt
		}
		if metadata.CompletionTokens == nil {
			metadata.CompletionTokens = completion
		}
		RecordUsage(ctx, metadata.PromptTokens, metadata.CompletionTokens)
		status := tracked.status
		if status == 0 {
			status = http.StatusOK
		}
		outcome := OutcomeFailure
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			outcome = OutcomeSuccess
		}
		if outcome == OutcomeFailure && metadata.ErrorCode == nil {
			code := fallbackHTTPErrorCode(status)
			metadata.ErrorCode = &code
		}
		var firstByteMS *int64
		if metadata.IsStream && !tracked.firstBodyAt.IsZero() {
			value := tracked.firstBodyAt.Sub(started).Milliseconds()
			firstByteMS = &value
		}
		record := RequestRecord{
			RequestID: requestID, Endpoint: request.URL.Path, ModelID: metadata.ModelID,
			AccessKeyID: metadata.AccessKeyID, NVIDIAKeyID: metadata.NVIDIAKeyID,
			HTTPStatus: status, Outcome: outcome, ErrorCode: metadata.ErrorCode,
			IsStream: metadata.IsStream, QueueMS: metadata.QueueMS, FirstByteMS: firstByteMS,
			DurationMS: source.Now().Sub(started).Milliseconds(), AttemptCount: metadata.AttemptCount,
			PromptTokens: metadata.PromptTokens, CompletionTokens: metadata.CompletionTokens,
			UpstreamRequestID: metadata.UpstreamRequestID, CreatedAt: started,
		}
		if err := recorder.Record(context.WithoutCancel(ctx), record); err != nil {
			logger.Error("record request metadata failed", "request_id", requestID, "error", err)
		}
	})
}

func fallbackHTTPErrorCode(status int) string {
	switch {
	case status >= http.StatusInternalServerError:
		return "http_5xx"
	case status >= http.StatusBadRequest:
		return "http_4xx"
	case status >= http.StatusMultipleChoices:
		return "http_3xx"
	default:
		return "http_error"
	}
}

type trackingWriter struct {
	http.ResponseWriter
	ctx             context.Context
	clock           clock.Clock
	started         time.Time
	firstBodyAt     time.Time
	status          int
	body            bytes.Buffer
	captureEnabled  bool
	captureComplete bool
	// tailCapture keeps only the trailing window of an SSE body instead of the
	// whole stream. It is decided from Content-Type once the status is known.
	tailCapture bool
}

func newTrackingWriter(writer http.ResponseWriter, ctx context.Context, started time.Time, source clock.Clock, endpoint string) *trackingWriter {
	_, captureEnabled := usageCaptureEndpoints[endpoint]
	return &trackingWriter{
		ResponseWriter:  writer,
		ctx:             ctx,
		clock:           source,
		started:         started,
		captureEnabled:  captureEnabled,
		captureComplete: captureEnabled,
	}
}

func (w *trackingWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.disableCaptureIfIneligible()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *trackingWriter) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
		w.disableCaptureIfIneligible()
	}
	if w.firstBodyAt.IsZero() && len(payload) > 0 {
		w.firstBodyAt = w.clock.Now()
	}
	if w.captureComplete {
		if w.tailCapture {
			w.appendTail(payload)
		} else {
			remaining := usageCaptureLimit - w.body.Len()
			if len(payload) <= remaining {
				_, _ = w.body.Write(payload)
			} else {
				w.captureComplete = false
				w.body.Reset()
			}
		}
	}
	return w.ResponseWriter.Write(payload)
}

func (w *trackingWriter) disableCaptureIfIneligible() {
	if w.status < http.StatusOK || w.status >= http.StatusMultipleChoices {
		w.captureEnabled = false
		w.captureComplete = false
		w.body.Reset()
		return
	}
	mediaType, _, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
	if err != nil || !isUsageMediaType(mediaType) {
		w.captureEnabled = false
		w.captureComplete = false
		w.body.Reset()
		return
	}
	w.tailCapture = strings.EqualFold(mediaType, "text/event-stream")
}

// appendTail keeps the last usageTailCaptureLimit bytes of the stream. It trims
// at an event boundary so the retained window starts on a whole event; a
// half-event at the front would be skipped by lastSSEEventData anyway, but
// trimming keeps the buffer's contents meaningful on inspection.
func (w *trackingWriter) appendTail(payload []byte) {
	_, _ = w.body.Write(payload)
	if w.body.Len() <= usageTailCaptureLimit {
		return
	}
	retained := w.body.Bytes()[w.body.Len()-usageTailCaptureLimit:]
	if boundary := bytes.Index(retained, []byte("\n\n")); boundary >= 0 {
		retained = retained[boundary+2:]
	}
	// Copy before Reset: retained aliases the buffer's storage.
	kept := make([]byte, len(retained))
	copy(kept, retained)
	w.body.Reset()
	_, _ = w.body.Write(kept)
}

func isUsageMediaType(mediaType string) bool {
	return strings.EqualFold(mediaType, "application/json") ||
		strings.EqualFold(mediaType, "text/event-stream")
}

func (w *trackingWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *trackingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *trackingWriter) SetErrorCode(code string) {
	SetErrorCode(w.ctx, code)
}

func parseUsage(payload []byte, complete, enabled bool, status int, contentType string) (*int64, *int64) {
	if !enabled || !complete || len(payload) == 0 || status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, nil
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, nil
	}
	var body []byte
	switch {
	case strings.EqualFold(mediaType, "application/json"):
		body = payload
	case strings.EqualFold(mediaType, "text/event-stream"):
		body = lastSSEEventData(payload)
	default:
		return nil, nil
	}
	var envelope struct {
		Usage struct {
			PromptTokens     *int64 `json:"prompt_tokens"`
			CompletionTokens *int64 `json:"completion_tokens"`
			InputTokens      *int64 `json:"input_tokens"`
			OutputTokens     *int64 `json:"output_tokens"`
		} `json:"usage"`
		Response struct {
			Usage struct {
				InputTokens  *int64 `json:"input_tokens"`
				OutputTokens *int64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, nil
	}
	if envelope.Usage.PromptTokens == nil {
		envelope.Usage.PromptTokens = envelope.Usage.InputTokens
	}
	if envelope.Usage.CompletionTokens == nil {
		envelope.Usage.CompletionTokens = envelope.Usage.OutputTokens
	}
	if envelope.Usage.PromptTokens == nil {
		envelope.Usage.PromptTokens = envelope.Response.Usage.InputTokens
	}
	if envelope.Usage.CompletionTokens == nil {
		envelope.Usage.CompletionTokens = envelope.Response.Usage.OutputTokens
	}
	return envelope.Usage.PromptTokens, envelope.Usage.CompletionTokens
}

// lastSSEEventData returns the JSON payload of the final data: event in an SSE
// stream, skipping empty and [DONE] termination events. Chat-completions and
// Responses streams both carry token usage in a trailing event.
func lastSSEEventData(payload []byte) []byte {
	trimmed := bytes.TrimRight(payload, "\r\n")
	events := bytes.Split(trimmed, []byte("\n\n"))
	for index := len(events) - 1; index >= 0; index-- {
		var data []byte
		for _, line := range bytes.Split(events[index], []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data:")) {
				data = append(data, bytes.TrimPrefix(line, []byte("data:"))...)
			}
		}
		event := bytes.TrimSpace(data)
		if len(event) == 0 || bytes.Equal(event, []byte("[DONE]")) {
			continue
		}
		return event
	}
	return nil
}

func newRequestID() string {
	var value [16]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return "req-" + hex.EncodeToString(value[:])
}
