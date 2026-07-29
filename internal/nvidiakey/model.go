package nvidiakey

import "time"

type Key struct {
	ID                  int64
	DisplayPrefix       string
	DisplaySuffix       string
	Enabled             bool
	AuthInvalid         bool
	CooldownUntil       *time.Time
	CooldownReason      string
	CooldownLevel       int
	ConsecutiveFailures int
	LastSuccessAt       *time.Time
	LastErrorAt         *time.Time
	LastErrorCode       string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type ImportStatus string

const (
	ImportStatusImported               ImportStatus = "imported"
	ImportStatusDuplicate              ImportStatus = "duplicate"
	ImportStatusInvalid                ImportStatus = "invalid"
	ImportStatusTemporarilyUnavailable ImportStatus = "temporarily_unavailable"
	ImportStatusIndeterminate          ImportStatus = "indeterminate"
)

type ImportResult struct {
	Line   int
	Status ImportStatus
	Reason string
	Masked string
	Key    *Key
}
