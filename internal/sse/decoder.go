package sse

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

const MaxEventSize = 4 << 20 // 4 MiB

var ErrEventTooLarge = errors.New("SSE event exceeds maximum size")

type Decoder struct {
	reader    *bufio.Reader
	current   Event
	buffer    bytes.Buffer
	bytesRead int
}

func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{reader: bufio.NewReader(r)}
}

func (d *Decoder) Decode() (Event, error) {
	d.current = Event{}
	d.buffer.Reset()
	d.bytesRead = 0

	for {
		line, err := d.readLine()
		if err != nil {
			if err == io.EOF && !d.current.IsEmpty() {
				return d.current, nil
			}
			return Event{}, err
		}

		if len(line) == 0 {
			if d.current.IsEmpty() {
				continue
			}
			event := d.current
			d.current = Event{}
			d.bytesRead = 0
			return event, nil
		}

		if err := d.parseLine(line); err != nil {
			return Event{}, err
		}
	}
}

func (d *Decoder) readLine() ([]byte, error) {
	line, err := d.reader.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read SSE line: %w", err)
	}

	d.bytesRead += len(line)
	if d.bytesRead > MaxEventSize {
		return nil, ErrEventTooLarge
	}

	line = bytes.TrimSuffix(line, []byte("\r\n"))
	line = bytes.TrimSuffix(line, []byte("\n"))

	if err == io.EOF && len(line) == 0 {
		return nil, io.EOF
	}

	return line, nil
}

func (d *Decoder) parseLine(line []byte) error {
	if len(line) > 0 && line[0] == ':' {
		d.current.Comments = append(d.current.Comments, string(line[1:]))
		return nil
	}

	field, value, hasColon := bytes.Cut(line, []byte(":"))
	if !hasColon {
		field = line
		value = nil
	} else {
		value = bytes.TrimPrefix(value, []byte(" "))
	}

	switch string(field) {
	case "event":
		d.current.Event = string(value)
	case "id":
		d.current.ID = string(value)
	case "retry":
		d.current.Retry = string(value)
	case "data":
		d.current.Data = append(d.current.Data, string(value))
	default:
		// Ignore unknown fields per SSE spec
	}

	return nil
}

func JoinData(data []string) string {
	return strings.Join(data, "\n")
}
