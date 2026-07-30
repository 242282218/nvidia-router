package sse

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func FuzzDecoder(f *testing.F) {
	seeds := []string{
		"data: hello\n\n",
		"event: test\ndata: {}\n\n",
		": comment\ndata: value\nid: 123\nretry: 1000\n\n",
		"data: line1\ndata: line2\n\n",
		"data: [DONE]\n\n",
		"\n\n",
		"data\n\n",
		"data:\n\n",
		"data: \n\n",
		"unknown: field\n\n",
		strings.Repeat("data: chunk\n", 100) + "\n",
	}

	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		decoder := NewDecoder(bytes.NewReader(input))
		var totalSize int
		for {
			event, err := decoder.Decode()
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, ErrEventTooLarge) {
					break
				}
				// Other errors are acceptable
				return
			}
			// Event size should never grow unbounded
			for _, data := range event.Data {
				totalSize += len(data)
			}
			if totalSize > MaxEventSize*2 {
				t.Fatal("decoder allowed unbounded memory growth")
			}
		}
	})
}
