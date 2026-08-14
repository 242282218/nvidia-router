package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"nvidia-router/internal/eventhub"
	"nvidia-router/internal/observability"
	"nvidia-router/internal/sse"
)

const (
	defaultEventStreamSubscribers = 32
	eventStreamWriteTimeout       = 30 * time.Second
)

// requestEventDTO is the wire shape pushed to the live view: a compact,
// metadata-only view of a completed request. It deliberately mirrors the
// observability RequestLog fields but carries no bodies (none are captured).
type requestEventDTO struct {
	RequestID    string  `json:"request_id"`
	Endpoint     string  `json:"endpoint"`
	ModelID      string  `json:"model_id,omitempty"`
	AccessKeyID  *int64  `json:"access_key_id,omitempty"`
	NVIDIAKeyID  *int64  `json:"nvidia_key_id,omitempty"`
	HTTPStatus   int     `json:"http_status"`
	Outcome      string  `json:"outcome"`
	ErrorCode    *string `json:"error_code,omitempty"`
	IsStream     bool    `json:"is_stream"`
	QueueMS      int64   `json:"queue_ms"`
	FirstByteMS  *int64  `json:"first_byte_ms,omitempty"`
	FirstTokenMS *int64  `json:"first_token_ms,omitempty"`
	DurationMS   int64   `json:"duration_ms"`
	CreatedAt    string  `json:"created_at"`
}

// RequestEventLine serializes one recorded request into a ready-to-write SSE
// event (`event: request\ndata: {...}\n\n`). It runs once per record at publish
// time so every connected subscriber writes the same bytes without re-marshal.
func RequestEventLine(record observability.RequestRecord) string {
	dto := requestEventDTO{
		RequestID: record.RequestID, Endpoint: record.Endpoint, ModelID: record.ModelID,
		AccessKeyID: record.AccessKeyID, NVIDIAKeyID: record.NVIDIAKeyID,
		HTTPStatus: record.HTTPStatus, Outcome: record.Outcome, ErrorCode: record.ErrorCode,
		IsStream: record.IsStream, QueueMS: record.QueueMS,
		FirstByteMS: record.FirstByteMS, FirstTokenMS: record.FirstTokenMS,
		DurationMS: record.DurationMS, CreatedAt: record.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	payload, _ := json.Marshal(dto)
	return eventLine("request", string(payload))
}

// eventStreamSource is the subscribe side the live-view handler needs; the Hub
// satisfies it.
type eventStreamSource interface {
	Subscribe() (<-chan eventhub.Event, func())
}

// EventStream streams recorded request events to the admin live view using
// Server-Sent Events. It lives under RequireManagement so only an authenticated
// admin can open a connection.
type EventStream struct {
	hub   eventStreamSource
	slots chan struct{}
}

func NewEventStream(hub eventStreamSource, limits ...int) *EventStream {
	limit := defaultEventStreamSubscribers
	if len(limits) > 0 && limits[0] > 0 {
		limit = limits[0]
	}
	return &EventStream{hub: hub, slots: make(chan struct{}, limit)}
}

func (h *EventStream) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/admin/api/events/stream" || request.Method != http.MethodGet {
		http.NotFound(writer, request)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		http.Error(writer, "streaming not supported", http.StatusInternalServerError)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		http.Error(writer, "event stream subscriber limit reached", http.StatusServiceUnavailable)
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	// Heartbeat comment keeps intermediaries from idle-closing the stream while
	// no request events are flowing.
	if err := sse.SetWriteDeadline(writer, eventStreamWriteTimeout); err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := writer.Write([]byte(": connected\n\n")); err != nil {
		return
	}
	if err := sse.FlushWithDeadline(writer, flusher, eventStreamWriteTimeout); err != nil {
		return
	}

	events, unsubscribe := h.hub.Subscribe()
	defer unsubscribe()
	ctx := request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := sse.SetWriteDeadline(writer, eventStreamWriteTimeout); err != nil {
				return
			}
			if _, err := writer.Write([]byte(event.Serialized)); err != nil {
				return
			}
			if err := sse.FlushWithDeadline(writer, flusher, eventStreamWriteTimeout); err != nil {
				return
			}
		}
	}
}

// eventLine builds a single serialized SSE event: `data: <payload>\n\n`.
func eventLine(eventType, payload string) string {
	var sb strings.Builder
	if eventType != "" {
		sb.WriteString("event: " + eventType + "\n")
	}
	for _, line := range strings.Split(payload, "\n") {
		sb.WriteString("data: " + line + "\n")
	}
	sb.WriteString("\n")
	return sb.String()
}
