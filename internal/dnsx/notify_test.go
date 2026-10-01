package dnsx

import (
	"sync"
	"testing"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
	"github.com/miekg/dns"
)

func TestNotifyCoalescesBursts(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	listener := NewNotifyListener(
		config.NotifySettings{Enabled: true, RequireTSIG: false, RefreshCooldownSeconds: 0},
		nil,
		func(zone string) {
			mu.Lock()
			calls = append(calls, zone)
			mu.Unlock()
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
		},
	)

	wire := func() []byte {
		msg := new(dns.Msg)
		msg.SetQuestion("test.example.", dns.TypeSOA)
		msg.Opcode = dns.OpcodeNotify
		b, err := msg.Pack()
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	listener.HandleNotify(wire(), "127.0.0.1:1", "udp")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("callback did not start")
	}

	listener.HandleNotify(wire(), "127.0.0.1:1", "udp")
	listener.HandleNotify(wire(), "127.0.0.1:1", "udp")
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("expected 2 coalesced callbacks, got %d (%v)", len(calls), calls)
	}
}

func TestNotifyRejectsWrongOpcode(t *testing.T) {
	listener := NewNotifyListener(config.NotifySettings{RequireTSIG: false}, nil, nil)
	msg := new(dns.Msg)
	msg.SetQuestion("test.example.", dns.TypeSOA)
	b, _ := msg.Pack()
	resp := listener.HandleNotify(b, "127.0.0.1:1", "udp")
	if resp == nil {
		t.Fatal("expected error response")
	}
	var out dns.Msg
	if err := out.Unpack(resp); err != nil {
		t.Fatal(err)
	}
	if out.Rcode != dns.RcodeRefused {
		t.Fatalf("rcode %d", out.Rcode)
	}
}

func TestNotifyRequireTSIGRejectsUnsigned(t *testing.T) {
	listener := NewNotifyListener(config.NotifySettings{RequireTSIG: true}, nil, nil)
	msg := new(dns.Msg)
	msg.SetQuestion("test.example.", dns.TypeSOA)
	msg.Opcode = dns.OpcodeNotify
	b, _ := msg.Pack()
	resp := listener.HandleNotify(b, "127.0.0.1:1", "udp")
	if resp == nil {
		t.Fatal("expected response")
	}
	var out dns.Msg
	_ = out.Unpack(resp)
	if out.Rcode != dns.RcodeNotAuth {
		t.Fatalf("rcode %d", out.Rcode)
	}
}
