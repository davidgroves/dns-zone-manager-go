package live_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/live"
)

func TestBroadcastAppliedSkipsEmpty(t *testing.T) {
	h := live.NewHub(live.HubConfig{ChannelSize: 4})
	// Must not panic with nil hub / empty ops.
	live.BroadcastApplied(nil, "example.com.", "api", nil, nil)
	live.BroadcastApplied(h, "example.com.", "api", nil, nil)
}

func TestBroadcastAppliedSetsZoneAndOps(t *testing.T) {
	h := live.NewHub(live.HubConfig{ChannelSize: 4})
	sub, err := h.Subscribe("example.com.", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	serial := uint32(42)
	live.BroadcastApplied(h, "example.com.", "api", []dnsx.Operation{
		{Action: "replace", Name: "www.example.com.", Type: "A", Class: "IN", TTL: 60, Records: []string{"192.0.2.1"}},
	}, &serial)

	select {
	case raw := <-sub.Events:
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["type"] != "zone_change" || payload["zone"] != "example.com." || payload["trigger"] != "api" {
			t.Fatalf("payload=%v", payload)
		}
		ops, ok := payload["operations"].([]any)
		if !ok || len(ops) != 1 {
			t.Fatalf("operations=%v", payload["operations"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

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
