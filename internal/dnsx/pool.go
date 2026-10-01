package dnsx

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/metrics"
)

// Pool is a persistent TCP connection pool for DDNS exchanges with BIND.
type Pool struct {
	addr        string
	size        int
	idleTimeout time.Duration
	dialTimeout time.Duration

	mu     sync.Mutex
	conns  chan *pooledConn
	closed bool
	done   chan struct{}
	wg     sync.WaitGroup
}

type pooledConn struct {
	conn     *dns.Conn
	lastUsed time.Time
	// secrets applied for TSIG verification on this connection
	tsigSecret map[string]string
}

// NewPool creates a TCP pool. size is clamped to at least 1.
func NewPool(addr string, size int, idleTimeout, dialTimeout time.Duration) *Pool {
	if size < 1 {
		size = 1
	}
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	p := &Pool{
		addr:        addr,
		size:        size,
		idleTimeout: idleTimeout,
		dialTimeout: dialTimeout,
		conns:       make(chan *pooledConn, size),
		done:        make(chan struct{}),
	}
	if idleTimeout > 0 {
		p.wg.Add(1)
		go p.idleCloser()
	}
	return p
}

// Exchange borrows a connection, sends msg (TSIG already attached if needed),
// and returns the response. On I/O error the connection is discarded and
// dialed again once.
func (p *Pool) Exchange(ctx context.Context, msg *dns.Msg, tsigSecret map[string]string) (*dns.Msg, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveDDNSRoundtrip(time.Since(start).Seconds())
	}()

	pc, err := p.borrow(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := p.exchangeOn(ctx, pc, msg, tsigSecret)
	if err == nil {
		p.release(pc)
		return resp, nil
	}

	// Drop bad connection and retry once with a fresh dial.
	p.discard(pc)
	pc2, err2 := p.dial(ctx)
	if err2 != nil {
		return nil, fmt.Errorf("dns pool reconnect: %w (first error: %w)", err2, err)
	}
	resp, err = p.exchangeOn(ctx, pc2, msg, tsigSecret)
	if err != nil {
		p.discard(pc2)
		return nil, err
	}
	p.release(pc2)
	return resp, nil
}

func (p *Pool) exchangeOn(ctx context.Context, pc *pooledConn, msg *dns.Msg, tsigSecret map[string]string) (*dns.Msg, error) {
	pc.conn.TsigSecret = tsigSecret
	pc.tsigSecret = tsigSecret

	deadline, ok := ctx.Deadline()
	if ok {
		_ = pc.conn.SetDeadline(deadline)
	} else {
		_ = pc.conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	if err := pc.conn.WriteMsg(msg); err != nil {
		return nil, err
	}
	resp, err := pc.conn.ReadMsg()
	if err != nil {
		return nil, err
	}
	pc.lastUsed = time.Now()
	return resp, nil
}

func (p *Pool) borrow(ctx context.Context) (*pooledConn, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("dns pool closed")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case pc := <-p.conns:
		if p.idleTimeout > 0 && time.Since(pc.lastUsed) > p.idleTimeout {
			p.discard(pc)
			return p.dial(ctx)
		}
		return pc, nil
	default:
		return p.dial(ctx)
	}
}

func (p *Pool) dial(ctx context.Context) (*pooledConn, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("dns pool closed")
	}
	p.mu.Unlock()

	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := net.DialTimeout("tcp", p.addr, p.dialTimeout)
		ch <- result{c, err}
	}()

	select {
	case <-ctx.Done():
		go func() {
			r := <-ch
			if r.c != nil {
				_ = r.c.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		if tc, ok := r.c.(*net.TCPConn); ok {
			_ = tc.SetKeepAlive(true)
			_ = tc.SetKeepAlivePeriod(30 * time.Second)
		}
		return &pooledConn{
			conn:     &dns.Conn{Conn: r.c},
			lastUsed: time.Now(),
		}, nil
	}
}

func (p *Pool) release(pc *pooledConn) {
	if pc == nil {
		return
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		p.discard(pc)
		return
	}
	select {
	case p.conns <- pc:
	default:
		// Pool full — close surplus connection.
		p.discard(pc)
	}
}

func (p *Pool) discard(pc *pooledConn) {
	if pc == nil || pc.conn == nil {
		return
	}
	_ = pc.conn.Close()
}

func (p *Pool) idleCloser() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.idleTimeout / 2)
	if p.idleTimeout/2 < time.Second {
		ticker.Reset(time.Second)
	}
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.closeIdle()
		}
	}
}

func (p *Pool) closeIdle() {
	n := len(p.conns)
	for i := 0; i < n; i++ {
		select {
		case pc := <-p.conns:
			if time.Since(pc.lastUsed) > p.idleTimeout {
				p.discard(pc)
			} else {
				select {
				case p.conns <- pc:
				default:
					p.discard(pc)
				}
			}
		default:
			return
		}
	}
}

// Close drains and closes all pooled connections.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	close(p.done)
	p.mu.Unlock()
	p.wg.Wait()

	for {
		select {
		case pc := <-p.conns:
			p.discard(pc)
		default:
			return
		}
	}
}
