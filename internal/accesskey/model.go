package accesskey

import "time"

type Key struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Prefix        string     `json:"key_prefix"`
	CreatedAt     time.Time  `json:"created_at"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	RPMLimit      int        `json:"rpm_limit"`
	TPMLimit      int        `json:"tpm_limit"`
	MaxConcurrent int        `json:"max_concurrent"`
	// TokenBudget is the cumulative token cap (0 = unlimited). ConsumedTokens
	// tracks how much of it has been spent, so the admin surface can show a
	// budget meter next to the key list.
	TokenBudget    int64 `json:"token_budget"`
	ConsumedTokens int64 `json:"consumed_tokens"`
}

type CreatedKey struct {
	Key       Key
	Plaintext string
}

type AccessKeyIdentity struct {
	ID            int64
	Prefix        string
	ExpiresAt     *time.Time
	RPMLimit      int
	TPMLimit      int
	MaxConcurrent int
	// TokenBudget is the cumulative token cap (0 = unlimited); it gates each
	// request after the window limits so a key cannot outlive its total quota.
	TokenBudget    int64
	ConsumedTokens int64
}
