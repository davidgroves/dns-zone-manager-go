package dnsx

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestPoolExchangeEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveDNSConn(c)
		}
	}()

	pool := NewPool(ln.Addr().String(), 2, time.Second, time.Second)
	defer pool.Close()

	msg := new(dns.Msg)
	msg.SetQuestion("example.com.", dns.TypeA)
	msg.Id = 42

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := pool.Exchange(ctx, msg, nil)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if resp.Id != 42 {
		t.Fatalf("got id %d want 42", resp.Id)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(resp.Answer))
	}
}

func TestPoolReconnectAfterBrokenConn(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var accepts atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n := accepts.Add(1)
			if n == 1 {
				// First connection: close immediately to force reconnect.
				_ = c.Close()
				continue
			}
			go serveDNSConn(c)
		}
	}()

	pool := NewPool(ln.Addr().String(), 1, time.Second, time.Second)
	defer pool.Close()

	msg := new(dns.Msg)
	msg.SetQuestion("example.com.", dns.TypeA)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := pool.Exchange(ctx, msg, nil)
	if err != nil {
		t.Fatalf("Exchange after reconnect: %v", err)
	}
	if resp == nil {
		t.Fatal("nil response")
	}
	if accepts.Load() < 2 {
		t.Fatalf("expected at least 2 accepts, got %d", accepts.Load())
	}
}

func serveDNSConn(c net.Conn) {
	defer c.Close()
	for {
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		var length uint16
		if err := binary.Read(c, binary.BigEndian, &length); err != nil {
			return
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(c, buf); err != nil {
			return
		}
		req := new(dns.Msg)
		if err := req.Unpack(buf); err != nil {
			return
		}
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.IPv4(1, 2, 3, 4),
		})
		out, err := resp.Pack()
		if err != nil {
			return
		}
		if err := binary.Write(c, binary.BigEndian, uint16(len(out))); err != nil {
			return
		}
		if _, err := c.Write(out); err != nil {
			return
		}
	}
}

func TestPoolReusesTSIGConnection(t *testing.T) {
	secret := map[string]string{dns.Fqdn("test"): "so6ZGir4GPAqINNh9U5c3A=="}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{
		Listener:   ln,
		Net:        "tcp",
		TsigSecret: secret,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			m := new(dns.Msg)
			m.SetReply(r)
			if st := w.TsigStatus(); st != nil {
				t.Errorf("TSIG: %v", st)
				return
			}
			m.SetTsig(dns.Fqdn("test"), dns.HmacSHA256, 300, time.Now().Unix())
			if err := w.WriteMsg(m); err != nil {
				t.Errorf("write: %v", err)
			}
		}),
	}
	done := make(chan error, 1)
	go func() { done <- srv.ActivateAndServe() }()
	defer func() {
		_ = srv.Shutdown()
		<-done
	}()

	pool := NewPool(ln.Addr().String(), 1, time.Second, time.Second)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		msg := new(dns.Msg)
		msg.SetQuestion("example.com.", dns.TypeA)
		msg.SetTsig(dns.Fqdn("test"), dns.HmacSHA256, 300, time.Now().Unix())
		resp, err := pool.Exchange(ctx, msg, secret)
		if err != nil {
			t.Fatalf("exchange %d: %v", i, err)
		}
		if resp.Rcode != dns.RcodeSuccess {
			t.Fatalf("exchange %d rcode %s", i, dns.RcodeToString[resp.Rcode])
		}
		if msg.IsTsig() == nil {
			t.Fatalf("exchange %d stripped the caller's TSIG", i)
		}
	}
}
