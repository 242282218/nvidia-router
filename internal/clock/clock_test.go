package clock

import (
	"testing"
	"time"
)

func TestRealClockNow(t *testing.T) {
	before := time.Now()
	now := RealClock{}.Now()
	after := time.Now()
	if now.Before(before) || now.After(after) {
		t.Fatalf("Now() = %v, outside [%v, %v]", now, before, after)
	}
}

func TestRealClockNewTimer(t *testing.T) {
	timer := RealClock{}.NewTimer(5 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-time.After(time.Second):
		t.Fatal("NewTimer did not fire")
	}
}

func TestRealClockAfterFunc(t *testing.T) {
	done := make(chan struct{})
	timer := RealClock{}.AfterFunc(5*time.Millisecond, func() { close(done) })
	defer timer.Stop()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("AfterFunc callback did not run")
	}
}
