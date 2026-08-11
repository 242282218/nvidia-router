package eventhub

import (
	"testing"
	"time"
)

func TestHubPublishReplaysRingToNewSubscriber(t *testing.T) {
	hub := New(10)
	hub.Publish(Event{Type: "request", Serialized: "a"})
	hub.Publish(Event{Type: "request", Serialized: "b"})

	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	first := <-ch
	second := <-ch
	if first.Serialized != "a" || second.Serialized != "b" {
		t.Fatalf("replay order = %q, %q; want a, b", first.Serialized, second.Serialized)
	}
}

func TestHubRingCapsAtMaxEvents(t *testing.T) {
	hub := New(3)
	for index := 0; index < 5; index++ {
		hub.Publish(Event{Type: "request", Serialized: string(rune('a' + index))})
	}
	if hub.Len() != 3 {
		t.Fatalf("ring length = %d, want 3", hub.Len())
	}
	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	got := []string{}
	for index := 0; index < 3; index++ {
		select {
		case event := <-ch:
			got = append(got, event.Serialized)
		case <-time.After(time.Second):
			t.Fatal("timed out reading ring replay")
		}
	}
	want := []string{"c", "d", "e"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ring replay = %v, want %v", got, want)
		}
	}
}

func TestHubBroadcastsAndDropsSlowSubscriber(t *testing.T) {
	hub := New(10)
	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	for range 200 {
		hub.Publish(Event{Type: "request", Serialized: "x"})
	}
	// The subscriber channel is bounded (64); a burst that outpaces the reader
	// must drop rather than block Publish. Drain what arrived and confirm the
	// hub is still responsive.
	drained := 0
	for {
		select {
		case <-ch:
			drained++
		default:
			goto done
		}
	}
done:
	hub.Publish(Event{Type: "request", Serialized: "final"})
	if drained >= 200 {
		t.Fatalf("bounded subscriber drained %d events, expected some drops", drained)
	}
}

func TestHubUnsubscribeStopsDelivery(t *testing.T) {
	hub := New(10)
	ch, unsubscribe := hub.Subscribe()
	hub.Publish(Event{Type: "request", Serialized: "before"})
	<-ch
	unsubscribe()
	hub.Publish(Event{Type: "request", Serialized: "after"})
	select {
	case event := <-ch:
		t.Fatalf("received event after unsubscribe: %q", event.Serialized)
	default:
	}
}
