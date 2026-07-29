package accesskey

import "time"

type Key struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"key_prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type CreatedKey struct {
	Key       Key
	Plaintext string
}

type AccessKeyIdentity struct {
	ID     int64
	Prefix string
}
