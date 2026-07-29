package keystate

import (
	"reflect"
	"testing"
)

func TestKeySnapshotContainsOnlySchedulingMetadata(t *testing.T) {
	typeOfSnapshot := reflect.TypeOf(KeySnapshot{})
	want := []string{"ID", "Enabled", "AuthInvalid", "CooldownUntil", "CooldownLevel", "ConsecutiveFailures"}
	if typeOfSnapshot.NumField() != len(want) {
		t.Fatalf("field count = %d, want %d", typeOfSnapshot.NumField(), len(want))
	}
	for index, name := range want {
		if got := typeOfSnapshot.Field(index).Name; got != name {
			t.Fatalf("field %d = %q, want %q", index, got, name)
		}
	}
}
