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
}
