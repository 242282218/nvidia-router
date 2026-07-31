package sse

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestDecoderParsesBasicEvent(t *testing.T) {
	input := "event: message\ndata: hello\nid: 123\n\n"
	decoder := NewDecoder(strings.NewReader(input))

	event, err := decoder.Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if event.Event != "message" || len(event.Data) != 1 || event.Data[0] != "hello" || event.ID != "123" {
		t.Fatalf("event = %+v", event)
	}

	_, err = decoder.Decode()
	if err != io.EOF {
		t.Fatalf("expected EOF after single event, got: %v", err)
	}
}

func TestDecoderHandlesMultilineData(t *testing.T) {
	input := "data: line 1\ndata: line 2\ndata: line 3\n\n"
	decoder := NewDecoder(strings.NewReader(input))

	event, err := decoder.Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(event.Data) != 3 {
		t.Fatalf("data lines = %d, want 3", len(event.Data))
	}
	joined := JoinData(event.Data)
	if joined != "line 1\nline 2\nline 3" {
		t.Fatalf("joined data = %q", joined)
	}
}

func TestDecoderHandlesBothLineEndings(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"LF only", "data: test\n\n"},
		{"CRLF", "data: test\r\n\r\n"},
		{"mixed", "data: test\r\n\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder := NewDecoder(strings.NewReader(test.input))
			event, err := decoder.Decode()
			if err != nil || len(event.Data) != 1 || event.Data[0] != "test" {
				t.Fatalf("event = %+v, err = %v", event, err)
			}
		})
	}
}

func TestDecoderHandlesComments(t *testing.T) {
	input := ": this is a comment\ndata: payload\n\n"
	decoder := NewDecoder(strings.NewReader(input))

	event, err := decoder.Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(event.Comments) != 1 || event.Comments[0] != " this is a comment" {
		t.Fatalf("comments = %v", event.Comments)
	}
	if len(event.Data) != 1 || event.Data[0] != "payload" {
		t.Fatalf("data = %v", event.Data)
	}
}

func TestDecoderSkipsEmptyLines(t *testing.T) {
	input := "\n\ndata: first\n\n\ndata: second\n\n"
	decoder := NewDecoder(strings.NewReader(input))

	event1, err := decoder.Decode()
	if err != nil || len(event1.Data) != 1 || event1.Data[0] != "first" {
		t.Fatalf("first event = %+v, err = %v", event1, err)
	}

	event2, err := decoder.Decode()
	if err != nil || len(event2.Data) != 1 || event2.Data[0] != "second" {
		t.Fatalf("second event = %+v, err = %v", event2, err)
	}
}

func TestDecoderHandlesUTF8AcrossChunks(t *testing.T) {
	// UTF-8 multi-byte character split across buffer boundaries
	input := "data: hello " + strings.Repeat("x", 4000) + "世界\n\n"
	decoder := NewDecoder(strings.NewReader(input))

	event, err := decoder.Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !strings.HasPrefix(event.Data[0], "hello ") || !strings.HasSuffix(event.Data[0], "世界") {
		t.Fatalf("UTF-8 handling failed")
	}
}

func TestDecoderRejectsOversizedEvent(t *testing.T) {
	input := "data: " + strings.Repeat("x", MaxEventSize) + "\n\n"
	decoder := NewDecoder(strings.NewReader(input))

	_, err := decoder.Decode()
	if err != ErrEventTooLarge {
		t.Fatalf("expected ErrEventTooLarge, got: %v", err)
	}
}

func TestDecoderRejectsOversizedEventWithoutReadingFullPayload(t *testing.T) {
	input := "data: " + strings.Repeat("x", MaxEventSize*2)
	reader := &countingReader{reader: strings.NewReader(input)}

	_, err := NewDecoder(reader).Decode()

	if !errors.Is(err, ErrEventTooLarge) {
		t.Fatalf("Decode error = %v, want ErrEventTooLarge", err)
	}
	if reader.bytesRead >= len(input) {
		t.Fatalf("Decode read %d bytes, want fewer than full payload %d", reader.bytesRead, len(input))
	}
}

func TestDecoderHandlesIncompleteEventAtEOF(t *testing.T) {
	input := "data: incomplete"
	decoder := NewDecoder(strings.NewReader(input))

	event, err := decoder.Decode()
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(event.Data) != 1 || event.Data[0] != "incomplete" {
		t.Fatalf("event = %+v", event)
	}
}

func TestDecoderIgnoresUnknownFields(t *testing.T) {
	input := "unknown: value\ndata: payload\n\n"
	decoder := NewDecoder(strings.NewReader(input))

	event, err := decoder.Decode()
	if err != nil || len(event.Data) != 1 || event.Data[0] != "payload" {
		t.Fatalf("event = %+v, err = %v", event, err)
	}
}

func TestDecoderHandlesFieldWithoutValue(t *testing.T) {
	input := "data\n\n"
	decoder := NewDecoder(strings.NewReader(input))

	event, err := decoder.Decode()
	if err != nil || len(event.Data) != 1 || event.Data[0] != "" {
		t.Fatalf("event = %+v, err = %v", event, err)
	}
}

func TestDecoderHandlesColonInValue(t *testing.T) {
	input := "data: key:value:more\n\n"
	decoder := NewDecoder(strings.NewReader(input))

	event, err := decoder.Decode()
	if err != nil || event.Data[0] != "key:value:more" {
		t.Fatalf("event = %+v, err = %v", event, err)
	}
}

type countingReader struct {
	reader    io.Reader
	bytesRead int
}

func (r *countingReader) Read(payload []byte) (int, error) {
	read, err := r.reader.Read(payload)
	r.bytesRead += read
	return read, err
}
