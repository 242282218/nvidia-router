package eventhub

import (
	"sync"
)

// Event is a broadcast unit consumed by the admin live view. Payload is an
// already-serialised JSON body; keeping it a string avoids re-marshalling per
// subscriber and lets the producer control the wire shape.
type Event struct {
	Type    string `json:"type"`
	Payload string `json:"-"`
	// Serialized is the full SSE line (e.g. "data: {...}\n\n"); producers build
	// it once and every subscriber writes the same bytes.
	Serialized string
}

// Hub is a broadcast hub with a bounded replay ring. Producers publish events;
// subscribers (SSE connections) get a snapshot of the ring on subscribe plus a
// live non-blocking channel. It is safe for concurrent use.
type Hub struct {
	mu          sync.Mutex
	ring        []Event
	subscribers map[*subscriber]struct{}
	maxEvents   int
}

type subscriber struct {
	ch       chan Event
	closed   chan struct{}
	closeOnce sync.Once
}

// New returns a Hub with an event ring capped at maxEvents (0 falls back to a
// sane default of 500).
func New(maxEvents int) *Hub {
	if maxEvents <= 0 {
		maxEvents = 500
	}
	return &Hub{
		ring:        make([]Event, 0, maxEvents),
		subscribers: make(map[*subscriber]struct{}),
		maxEvents:   maxEvents,
	}
}

// Publish appends event to the ring (evicting the oldest when full) and
// non-blocking-fans it out to every subscriber. A slow subscriber whose channel
// is full is dropped: the live view must never back-pressure request handling.
func (h *Hub) Publish(event Event) {
	h.mu.Lock()
	if len(h.ring) >= h.maxEvents {
		// Ring full: shift elements in place with memmove (copy) and overwrite last element.
		copy(h.ring, h.ring[1:])
		h.ring[len(h.ring)-1] = event
	} else {
		h.ring = append(h.ring, event)
	}
	for sub := range h.subscribers {
		select {
		case sub.ch <- event:
		default:
		}
	}
	h.mu.Unlock()
}

// Subscribe registers a subscriber with an initial replay of the events in the
// ring (newest-last), then returns a channel that receives live events until
// Unsubscribe is called. The returned channel is never closed by the hub; call
// Unsubscribe to stop it.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub := &subscriber{ch: make(chan Event, 64), closed: make(chan struct{})}
	h.subscribers[sub] = struct{}{}
	// Replay ring contents to the new subscriber before any subsequent live
	// events, so a freshly connected admin sees recent activity immediately.
	for _, event := range h.ring {
		select {
		case sub.ch <- event:
		default:
		}
	}
	unsubscribe := func() {
		sub.closeOnce.Do(func() {
			close(sub.closed)
			h.mu.Lock()
			delete(h.subscribers, sub)
			h.mu.Unlock()
		})
	}
	return sub.ch, unsubscribe
}

// Len reports the current number of ring entries (used by tests and the admin
// summary to surface buffer pressure).
func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.ring)
}
