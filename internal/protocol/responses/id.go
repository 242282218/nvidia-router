package responses

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
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
