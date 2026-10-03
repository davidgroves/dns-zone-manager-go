package notifications_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/davidgroves/dns-zone-manager-go/internal/notifications"
	"github.com/davidgroves/dns-zone-manager-go/internal/notifications/formatters"
)

func ptr[T any](v T) *T { return &v }

func sampleEvent() notifications.DnsChangeEvent {
	return notifications.DnsChangeEvent{
		Event: notifications.EventChangeApplied,
		Zone:  "test.example.",
		Operations: []notifications.ChangeOperation{
			{
				Action:  "add",
				Name:    "www.test.example.",
				RDType:  "A",
				RDClass: "IN",
				TTL:     ptr(uint32(300)),
				Records: []string{"10.0.0.1"},
			},
		},
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Trigger:   notifications.TriggerManual,
		Actor:     ptr("alice@example.com"),
		ActorName: ptr("Alice Example"),
		AuthType:  ptr("proxy"),
		ChangeID:  ptr("abc-123"),
		Rcode:     ptr("NOERROR"),
	}
}

func TestFormatGenericGolden(t *testing.T) {
	payload := formatters.FormatGeneric(sampleEvent(), "https://dns.example.com")
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["event"] != "change_applied" {
		t.Fatalf("event=%v", got["event"])
	}
	if got["timestamp"] != "2026-01-02T03:04:05+00:00" {
		t.Fatalf("timestamp=%v", got["timestamp"])
	}
	actor := got["actor"].(map[string]any)
	if actor["name"] != "Alice Example" {
		t.Fatalf("actor=%v", actor)
	}
	change := got["change"].(map[string]any)
	if change["link"] != "https://dns.example.com/?view=scheduled&change=abc-123" {
		t.Fatalf("link=%v", change["link"])
	}
	result := got["result"].(map[string]any)
	if result["success"] != true || result["rcode"] != "NOERROR" {
		t.Fatalf("result=%v", result)
	}
}

func TestFormatSlackGolden(t *testing.T) {
	payload := formatters.FormatSlack(sampleEvent(), "https://dns.example.com")
	if payload["text"] != "DNS change applied: test.example." {
		t.Fatalf("text=%v", payload["text"])
	}
	blocks, _ := json.Marshal(payload["blocks"])
	s := string(blocks)
	if !containsAll(s, "Alice Example") || !containsAll(s, "Manual") {
		t.Fatalf("blocks missing fields: %s", s)
	}
	if !containsAll(s, "change=abc-123") {
		t.Fatalf("missing link button: %s", s)
	}
}

func TestFormatTeamsGolden(t *testing.T) {
	payload := formatters.FormatTeams(sampleEvent(), "https://dns.example.com")
	if payload["type"] != "message" {
		t.Fatalf("type=%v", payload["type"])
	}
	raw, _ := json.Marshal(payload)
	s := string(raw)
	if !containsAll(s, "AdaptiveCard") || !containsAll(s, "Alice Example") {
		t.Fatalf("payload=%s", s)
	}
	if !containsAll(s, "Action.OpenUrl") {
		t.Fatalf("missing action: %s", s)
	}
}

func TestHMACAuthHeaders(t *testing.T) {
	var secret config.Secret
	secret.Set("s3cr3t")
	target := config.WebhookTarget{
		Name: "t",
		Auth: config.WebhookAuth{
			Type:      "hmac",
			Secret:    secret,
			Algorithm: "sha256",
		},
	}
	body := []byte(`{"ok":true}`)
	ts := "2026-01-02T03:04:05+00:00"
	headers, err := notifications.BuildAuthHeaders(target, body, ts)
	if err != nil {
		t.Fatal(err)
	}
	sig := headers["X-DNS-Signature"]
	if sig == "" || headers["X-DNS-Timestamp"] != ts {
		t.Fatalf("headers=%v", headers)
	}
	want, err := notifications.ComputeSignature("s3cr3t", "sha256", ts, body)
	if err != nil {
		t.Fatal(err)
	}
	if sig != want {
		t.Fatalf("sig=%q want=%q", sig, want)
	}
}

func TestBearerAuth(t *testing.T) {
	var secret config.Secret
	secret.Set("tok")
	target := config.WebhookTarget{
		Auth: config.WebhookAuth{Type: "bearer", Secret: secret},
	}
	h, err := notifications.BuildAuthHeaders(target, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if h["Authorization"] != "Bearer tok" {
		t.Fatalf("%v", h)
	}
}

func containsAll(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
