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
