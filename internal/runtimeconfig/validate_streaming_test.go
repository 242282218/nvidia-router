package runtimeconfig

import "testing"

// TestValidateMaxStreamingPerKeyBounds verifies the streaming quota setting is
// range-checked like the other numeric settings: the documented range is 1..10.
// Zero is the pre-migration sentinel and deliberately passes — the pool resolves
// it to the default of 2, so legacy snapshots are not rejected at startup.
func TestValidateMaxStreamingPerKeyBounds(t *testing.T) {
	for _, invalid := range []int{-1, 11, 100} {
		snapshot := validSnapshotForValidation()
		snapshot.MaxStreamingPerKey = invalid
		if err := Validate(snapshot); err == nil {
			t.Fatalf("Validate(max_streaming_per_key=%d) error = nil, want range error", invalid)
		}
	}
	for _, valid := range []int{0, 1, 10} {
		snapshot := validSnapshotForValidation()
		snapshot.MaxStreamingPerKey = valid
		if err := Validate(snapshot); err != nil {
			t.Fatalf("Validate(max_streaming_per_key=%d) error = %v, want nil", valid, err)
		}
	}
}

// TestValidateStreamTimeoutBounds locks the 1000..1800000 range of the streaming
// timeout split. Zero is the pre-migration sentinel (a snapshot not yet loaded
// from a migration-014 database) and deliberately passes, mirroring
// MaxStreamingPerKey; the budget layer resolves it to the documented default.
func TestValidateStreamTimeoutBounds(t *testing.T) {
	for _, field := range []string{"stream_first_token_timeout_ms", "stream_idle_timeout_ms"} {
		for _, invalid := range []int{999, 1800001} {
			snapshot := validSnapshotForValidation()
			if field == "stream_first_token_timeout_ms" {
				snapshot.StreamFirstTokenTimeoutMS = invalid
			} else {
				snapshot.StreamIdleTimeoutMS = invalid
			}
			if err := Validate(snapshot); err == nil {
				t.Fatalf("Validate(%s=%d) error = nil, want range error", field, invalid)
			}
		}
		for _, valid := range []int{0, 1000, 1800000} {
			snapshot := validSnapshotForValidation()
			if field == "stream_first_token_timeout_ms" {
				snapshot.StreamFirstTokenTimeoutMS = valid
			} else {
				snapshot.StreamIdleTimeoutMS = valid
			}
			if err := Validate(snapshot); err != nil {
				t.Fatalf("Validate(%s=%d) error = %v, want nil", field, valid, err)
			}
		}
	}
}
