package sse

import (
	"bytes"
	"testing"
)

func TestEncoderEncodeStandardEvent(t *testing.T) {
	var out bytes.Buffer
	enc := NewEncoder(&out)
	event := Event{
		Event:    "message",
		ID:       "123",
		Retry:    "1000",
		Comments: []string{"keepalive"},
		Data:     []string{`{"content":"hello"}`},
	}
	if err := enc.Encode(event); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := ":keepalive\nevent: message\nid: 123\nretry: 1000\ndata: {\"content\":\"hello\"}\n\n"
	if got := out.String(); got != want {
		t.Fatalf("encoded = %q, want %q", got, want)
	}
}

func TestEncoderEncodeMultiLineData(t *testing.T) {
	var out bytes.Buffer
	enc := NewEncoder(&out)
	event := Event{
		Data: []string{"line1", "line2"},
	}
	if err := enc.Encode(event); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := "data: line1\ndata: line2\n\n"
	if got := out.String(); got != want {
		t.Fatalf("encoded = %q, want %q", got, want)
	}
}

func TestEncoderWriteRaw(t *testing.T) {
	var out bytes.Buffer
	enc := NewEncoder(&out)
	if err := enc.WriteRaw(": ping\n"); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	want := ": ping\n"
	if got := out.String(); got != want {
		t.Fatalf("WriteRaw = %q, want %q", got, want)
	}
}

func TestEncoderSplitsDataContainingNewlines(t *testing.T) {
	// A data value with embedded newlines must be split into per-line data:
	// fields, otherwise a client EventSource would parse the injected newline
	// as an event boundary (audit: SSE data newline injection).
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"single value with LF", []string{"line1\nline2"}, "data: line1\ndata: line2\n\n"},
		{"CRLF normalized", []string{"a\r\nb"}, "data: a\ndata: b\n\n"},
		{"lone CR normalized", []string{"a\rb"}, "data: a\ndata: b\n\n"},
		{"multiline joined with other values", []string{"first", "x\ny", "last"}, "data: first\ndata: x\ndata: y\ndata: last\n\n"},
		{"no newline passthrough", []string{"plain"}, "data: plain\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := NewEncoder(&out).Encode(Event{Data: tc.in}); err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if got := out.String(); got != tc.want {
				t.Fatalf("encoded = %q, want %q", got, tc.want)
			}
		})
	}
}

func BenchmarkEncoderEncode(b *testing.B) {
	event := Event{
		Event: "chunk",
		ID:    "42",
		Data:  []string{`{"choices":[{"delta":{"content":"token","reasoning_content":null}}]}`},
	}
	var out bytes.Buffer
	enc := NewEncoder(&out)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out.Reset()
		_ = enc.Encode(event)
	}
}
