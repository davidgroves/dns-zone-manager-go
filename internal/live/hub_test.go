package live_test

import (
	"testing"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/live"
)

func TestHubBroadcastDropsSlowClient(t *testing.T) {
	h := live.NewHub(live.HubConfig{
		MaxConnections:      10,
		MaxConnectionsPerIP: 10,
		ChannelSize:         1,
		SendTimeout:         time.Second,
		PingInterval:        time.Hour,
	})

	sub, err := h.Subscribe("example.com.", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	// Fill the bounded channel.
	h.Broadcast("example.com.", map[string]any{"type": "zone_change", "n": 1})
	// Second broadcast should drop the slow subscriber (channel full).
	h.Broadcast("example.com.", map[string]any{"type": "zone_change", "n": 2})

	// After drop, further broadcasts should not deliver.
	select {
	case <-sub.Events:
		// first message may still be readable
	default:
	}
	// Give remove a moment; channel may still have the first item.
	time.Sleep(10 * time.Millisecond)

	h.Broadcast("example.com.", map[string]any{"type": "zone_change", "n": 3})

	// Drain whatever remains; n=3 must not appear after drop (or sub is closed/done).
	gotThird := false
	deadline := time.After(50 * time.Millisecond)
drain:
	for {
		select {
		case msg, ok := <-sub.Events:
			if !ok {
				break drain
			}
			if string(msg) != "" && containsN(string(msg), `"n":3`) {
				gotThird = true
			}
		case <-deadline:
			break drain
		}
	}
	if gotThird {
		t.Fatal("slow client should have been dropped before third broadcast")
	}
}

func TestHubMaxConnections(t *testing.T) {
	h := live.NewHub(live.HubConfig{MaxConnections: 1, MaxConnectionsPerIP: 10, ChannelSize: 2})
	s1, err := h.Subscribe("a.com.", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Unsubscribe()
	_, err = h.Subscribe("b.com.", "1.1.1.2")
	if !live.IsConnectionLimit(err) || live.LimitReason(err) != "max_connections" {
		t.Fatalf("err=%v", err)
	}
}

func TestHubMaxPerIP(t *testing.T) {
	h := live.NewHub(live.HubConfig{MaxConnections: 10, MaxConnectionsPerIP: 1, ChannelSize: 2})
	s1, err := h.Subscribe("a.com.", "9.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Unsubscribe()
	_, err = h.Subscribe("b.com.", "9.9.9.9")
	if !live.IsConnectionLimit(err) || live.LimitReason(err) != "max_per_ip" {
		t.Fatalf("err=%v", err)
	}
}

func containsN(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
