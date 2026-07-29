package app

import (
	"context"
	"testing"
)

func TestNew(t *testing.T) {
	app, err := New(context.Background(), Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if app == nil {
		t.Fatal("expected app")
	}
}
