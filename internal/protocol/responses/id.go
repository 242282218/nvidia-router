package responses

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

const responsesIDPrefix = "resp_"

// responsesIDBytes keeps IDs short while collision-resistant within the
// request lifecycle; responses are never persisted, so global uniqueness is
// not required.
const responsesIDBytes = 16

// NewResponseID generates a request-scoped stable response identifier. The ID
// only identifies the single response being produced and is never stored.
func NewResponseID() (string, error) {
	random := make([]byte, responsesIDBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate responses id: %w", err)
	}
	return responsesIDPrefix + base64.RawURLEncoding.EncodeToString(random), nil
}

// messageItemID derives a stable, request-scoped message item id from the
// response id. Chat streaming deltas carry no message-level id, so every event
// for the single assistant message references this synthesized value. Response
// ids are unique per request and never persisted, so the message id is too.
func messageItemID(responseID string) string {
	return "msg_" + strings.TrimPrefix(responseID, responsesIDPrefix)
}

// reasoningItemID derives a stable, request-scoped reasoning item id from the
// response id, mirroring messageItemID. Reasoning summaries are rare per
// request, so reusing the response-id derivation keeps ids unique and stable.
func reasoningItemID(responseID string) string {
	return "rs_" + strings.TrimPrefix(responseID, responsesIDPrefix)
}
