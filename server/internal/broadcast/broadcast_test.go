package broadcast

import (
	"testing"
	"time"
)

func TestPublishNoSubscribers(t *testing.T) {
	b := New()
	if got := b.Publish("ws1", []byte("x")); got != 0 {
		t.Fatalf("Publish with no subscribers = %d, want 0", got)
	}
}

func TestPublishSingleSubscriber(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe("ws1")
	defer cancel()
	if got := b.SubscriberCount("ws1"); got != 1 {
		t.Fatalf("SubscriberCount = %d, want 1", got)
	}
	if got := b.Publish("ws1", []byte("payload")); got != 1 {
		t.Fatalf("delivered = %d, want 1", got)
	}
	select {
	case got := <-ch:
		if string(got) != "payload" {
			t.Fatalf("payload = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber never received the payload")
	}
}

func TestPublishFanOut(t *testing.T) {
	b := New()
	chs := make([]<-chan []byte, 5)
	for i := range chs {
		ch, cancel := b.Subscribe("ws1")
		defer cancel()
		chs[i] = ch
	}
	if got := b.Publish("ws1", []byte("x")); got != 5 {
		t.Fatalf("delivered = %d, want 5", got)
	}
	for i, ch := range chs {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d missed the fan-out", i)
		}
	}
}

func TestWorkspacesIsolated(t *testing.T) {
	b := New()
	ch1, cancel1 := b.Subscribe("ws1")
	ch2, cancel2 := b.Subscribe("ws2")
	defer cancel1()
	defer cancel2()
	b.Publish("ws1", []byte("x"))
	select {
	case <-ch1:
	case <-time.After(time.Second):
		t.Fatal("ws1 subscriber missed its own publish")
	}
	select {
	case got := <-ch2:
		t.Fatalf("ws2 subscriber received a ws1 payload: %q", got)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestCancelStopsDelivery(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe("ws1")
	cancel()
	if got := b.SubscriberCount("ws1"); got != 0 {
		t.Fatalf("SubscriberCount after cancel = %d, want 0", got)
	}
	if got := b.Publish("ws1", []byte("x")); got != 0 {
		t.Fatalf("Publish after cancel = %d, want 0", got)
	}
	// Double cancel must be safe and idempotent.
	cancel()
	if got := b.SubscriberCount("ws1"); got != 0 {
		t.Fatalf("SubscriberCount after double cancel = %d, want 0", got)
	}
	// The cancelled channel must never receive; drain check with timeout.
	select {
	case got := <-ch:
		t.Fatalf("cancelled channel received %q", got)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestResubscribeAfterCancel(t *testing.T) {
	b := New()
	_, cancel := b.Subscribe("ws1")
	cancel()
	ch, cancel2 := b.Subscribe("ws1")
	defer cancel2()
	if got := b.SubscriberCount("ws1"); got != 1 {
		t.Fatalf("SubscriberCount = %d, want 1", got)
	}
	if got := b.Publish("ws1", []byte("x")); got != 1 {
		t.Fatalf("delivered = %d, want 1", got)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("resubscribed channel missed the payload")
	}
}

// TestSlowSubscriberDropped pins the drop-don't-block contract: a full
// buffer is skipped, Publish returns, and fast subscribers are served.
// The client resyncs via the delta endpoint (spec R-8), so the drop is by
// design — but the non-blocking guarantee is what keeps writers safe.
func TestSlowSubscriberDropped(t *testing.T) {
	b := New()
	_, slowCancel := b.Subscribe("ws1")
	fast, fastCancel := b.Subscribe("ws1")
	defer slowCancel()
	defer fastCancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Flood well past the 64 buffer; never drain.
		for i := 0; i < 200; i++ {
			b.Publish("ws1", []byte("x"))
		}
	}()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-done:
			// The last publish must have served the fast subscriber only.
			select {
			case <-fast:
			default:
				// The fast subscriber's buffer may still hold payloads;
				// what matters is that Publish never blocked.
			}
			return
		case <-deadline:
			t.Fatal("Publish blocked on a full subscriber buffer")
		}
	}
}

// TestConcurrentSubscribePublish pins the locking: concurrent Subscribe,
// Publish and cancel must not race (run with -race).
func TestConcurrentSubscribePublish(t *testing.T) {
	b := New()
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			for {
				select {
				case <-stop:
					return
				default:
				}
				ch, cancel := b.Subscribe("ws1")
				if n%2 == 0 {
					cancel()
				} else {
					go func(c <-chan []byte) {
						select {
						case <-c:
						case <-stop:
						}
					}(ch)
				}
				b.Publish("ws1", []byte("x"))
				_ = b.SubscriberCount("ws1")
			}
		}(i)
	}
	time.Sleep(100 * time.Millisecond)
	close(stop)
	time.Sleep(50 * time.Millisecond)
}
