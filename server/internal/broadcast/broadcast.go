// Package broadcast fans out committed change records to realtime
// subscribers, grouped by workspace.
package broadcast

import (
	"sync"

	"converge/internal/metrics"
)

var slowDisconnects = metrics.Default.NewCounterVec("converge_realtime_slow_disconnects_total",
	"Realtime subscribers disconnected for falling a full buffer behind.").With()

// Event is one committed record on its way to subscribers.
type Event struct {
	// Data is the wire record (JSON).
	Data []byte
	// Seq is the record's workspace sequence, so a subscriber that must
	// not see Data can still advance its cursor past it.
	Seq string
	// To addresses the record to one account; empty means every
	// subscriber of the workspace.
	To string
}

// Broadcaster is an in-process pub/sub for one API instance.
//
// Channels are buffered; a subscriber whose buffer is full is
// disconnected rather than blocking the writer: its channel is closed,
// the stream ends, and the client reconnects and fills the gap from the
// delta endpoint (authoritative per spec R-8). Skipping the payload
// instead would leave a silent hole the client cannot see.
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

func New() *Broadcaster {
	return &Broadcaster{subs: make(map[string]map[chan Event]struct{})}
}

// subscriberBuffer is the per-subscriber backlog before disconnection.
const subscriberBuffer = 64

// Subscribe registers a channel for the workspace and returns it with a
// cancel function. The channel is closed if the subscriber falls a full
// buffer behind; cancel never closes it.
func (b *Broadcaster) Subscribe(workspaceID string) (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	set := b.subs[workspaceID]
	if set == nil {
		set = make(map[chan Event]struct{})
		b.subs[workspaceID] = set
	}
	set[ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if set := b.subs[workspaceID]; set != nil {
			if _, ok := set[ch]; ok {
				delete(set, ch)
				if len(set) == 0 {
					delete(b.subs, workspaceID)
				}
			}
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

// Publish sends the event to every subscriber of the workspace and
// returns how many received it. Subscribers with a full buffer are
// disconnected (see Broadcaster).
func (b *Broadcaster) Publish(workspaceID string, ev Event) int {
	b.mu.RLock()
	delivered := 0
	var overflowed []chan Event
	for ch := range b.subs[workspaceID] {
		select {
		case ch <- ev:
			delivered++
		default:
			overflowed = append(overflowed, ch)
		}
	}
	b.mu.RUnlock()
	if len(overflowed) > 0 {
		b.disconnect(workspaceID, overflowed)
	}
	return delivered
}

// disconnect removes and closes the given channels. Closing only under
// the write lock, and only while still registered, means no Publish can
// be sending on them and a concurrent disconnect cannot close twice.
func (b *Broadcaster) disconnect(workspaceID string, chs []chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	set := b.subs[workspaceID]
	for _, ch := range chs {
		if _, ok := set[ch]; ok {
			delete(set, ch)
			close(ch)
			slowDisconnects.Inc()
		}
	}
	if set != nil && len(set) == 0 {
		delete(b.subs, workspaceID)
	}
}

// SubscriberCount reports how many live subscribers a workspace has.
func (b *Broadcaster) SubscriberCount(workspaceID string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[workspaceID])
}

// Subscribers reports the live subscribers across all workspaces.
func (b *Broadcaster) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := 0
	for _, set := range b.subs {
		n += len(set)
	}
	return n
}
