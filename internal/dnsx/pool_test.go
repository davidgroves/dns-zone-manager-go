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
