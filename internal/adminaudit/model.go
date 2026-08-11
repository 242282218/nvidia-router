package adminaudit

import "time"

// Entry is a single immutable record of a mutating admin action. The Detail
// payload is a compact JSON document that must never contain raw secrets
// (NVIDIA key material, access-key plaintexts).
type Entry struct {
	ID         int64
	Action     string
	TargetType string
	TargetID   string
	Detail     string
	SessionID  *string
	ClientIP   string
	CreatedAt  time.Time
}
