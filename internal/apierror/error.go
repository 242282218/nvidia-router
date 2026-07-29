package apierror

import "time"

type Error struct {
	Status     int
	Type       string
	Code       string
	Message    string
	Param      *string
	RetryAfter time.Duration
	Cause      error
}

func (err Error) Error() string {
	return err.Message
}
