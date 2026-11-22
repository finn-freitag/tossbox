// Package events is an in-process pub/sub broker feeding /api/events (SSE).
package events

import (
	"encoding/json"
	"sync"
)

// Event is one server-sent event.
type Event struct {
	Type string // device | device_deleted | node
	Data json.RawMessage
	// Audience: OwnerID receives it; admins receive everything unless AdminOnly
	// is false and OwnerID is set (admins still get it). AdminOnly limits it to admins.
	OwnerID   string
	AdminOnly bool
}

// Subscriber receives events.
type Subscriber struct {
	UserID  string
	IsAdmin bool
	C       chan Event
}

// Broker fans events out to subscribers. Slow subscribers drop events (the UI
// re-fetches on reconnect, so this is safe).
type Broker struct {
	mu   sync.Mutex
	subs map[*Subscriber]struct{}
}

// NewBroker creates a broker.
func NewBroker() *Broker { return &Broker{subs: map[*Subscriber]struct{}{}} }

// Subscribe registers a subscriber.
func (b *Broker) Subscribe(userID string, isAdmin bool) *Subscriber {
	s := &Subscriber{UserID: userID, IsAdmin: isAdmin, C: make(chan Event, 64)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Unsubscribe removes a subscriber.
func (b *Broker) Unsubscribe(s *Subscriber) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
}

// Publish sends an event to all matching subscribers.
func (b *Broker) Publish(typ string, data any, ownerID string, adminOnly bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	ev := Event{Type: typ, Data: raw, OwnerID: ownerID, AdminOnly: adminOnly}
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if !s.IsAdmin && (adminOnly || s.UserID != ownerID) {
			continue
		}
		select {
		case s.C <- ev:
		default:
		}
	}
}
