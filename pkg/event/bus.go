package event

import (
	"sync"
)

type Subscriber chan Event

type EventBus struct {
	mu          sync.RWMutex
	subscribers map[string][]Subscriber
}

var GlobalBus = NewEventBus()

func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[string][]Subscriber),
	}
}

func (eb *EventBus) Subscribe(eventType string) Subscriber {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	ch := make(Subscriber, 100)
	eb.subscribers[eventType] = append(eb.subscribers[eventType], ch)
	return ch
}

func (eb *EventBus) Publish(eventType string, payload interface{}) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	subs, exists := eb.subscribers[eventType]
	if !exists {
		return
	}

	ev := Event{
		Type:    eventType,
		Payload: payload,
	}

	for _, sub := range subs {
		// Non-blocking write to avoid hanging if subscriber is slow
		select {
		case sub <- ev:
		default:
			// Dropped event if channel is full
		}
	}
}
