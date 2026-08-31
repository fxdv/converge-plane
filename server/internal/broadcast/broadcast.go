// Package broadcast fans out committed change records to realtime
// subscribers, grouped by workspace.
package broadcast

import "sync"

// Broadcaster is an in-process pub/sub for one API instance.
//
// Channels are buffered; a slow subscriber is dropped rather than
// blocking the writer (clients resynchronize via the delta endpoint,
// which remains authoritative per spec R-8).
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{}
}

func New() *Broadcaster {
	return &Broadcaster{subs: make(map[string]map[chan []byte]struct{})}
}

// Subscribe registers a channel for the workspace and returns it with a
// cancel function. The channel is buffered to 64 payloads.
func (b *Broadcaster) Subscribe(workspaceID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	set := b.subs[workspaceID]
	if set == nil {
		set = make(map[chan []byte]struct{})
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

// Publish sends the payload to every subscriber of the workspace.
// Full subscriber buffers are skipped (the drop is logged by callers).
func (b *Broadcaster) Publish(workspaceID string, payload []byte) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	delivered := 0
	for ch := range b.subs[workspaceID] {
		select {
		case ch <- payload:
			delivered++
		default:
		}
	}
	return delivered
}

// SubscriberCount reports how many live subscribers a workspace has.
func (b *Broadcaster) SubscriberCount(workspaceID string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[workspaceID])
}
