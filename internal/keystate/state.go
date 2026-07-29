package keystate

import "time"

type KeySnapshot struct {
	ID                  int64
	Enabled             bool
	AuthInvalid         bool
	CooldownUntil       *time.Time
	CooldownLevel       int
	ConsecutiveFailures int
}

type ModelBlock struct {
	KeyID   int64
	ModelID int64
}
