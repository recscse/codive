package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// reconnectWindow is how long a shim keeps trying to reach (or restart) the
// daemon after the connection drops.
const reconnectWindow = 25 * time.Second

type envelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func parseEnvelope(line []byte) (e envelope) {
	_ = json.Unmarshal(bytes.TrimSpace(line), &e)
	if string(e.ID) == "null" {
		e.ID = nil
	}
	return e
}

// proxy copies MCP messages between a client's stdio and a daemon session.
// If the daemon goes away it opens a new session and replays the client's
// initialize handshake, so the client never notices a daemon restart (an
// upgrade, a crash, an idle shutdown) beyond the requests that were in flight,
// which are answered with an error.
type proxy struct {
	out  io.Writer
	outM sync.Mutex
	dial func() (net.Conn, error)

	mu              sync.Mutex
	conn            net.Conn
	gen             int
	pending         map[string]json.RawMessage // in-flight request ids
	initLine        []byte
	initializedLine []byte
	swallow         string // id of a replayed initialize, whose reply the client must not see
	closed          bool
	fatal           chan error
}

// Proxy forwards in to a daemon session and the session's output to out, until
// in ends or the daemon can't be reached again. dial opens a session.
func Proxy(in io.Reader, out io.Writer, dial func() (net.Conn, error)) error {
	p := &proxy{out: out, dial: dial, pending: map[string]json.RawMessage{}, fatal: make(chan error, 1)}
	c, err := dial()
	if err != nil {
		return err
	}
	p.conn, p.gen = c, 1
	go p.readLoop(c, 1)

	lines := make(chan []byte)
	go func() {
		defer close(lines)
		r := bufio.NewReaderSize(in, 1<<20)
		for {
			b, err := r.ReadBytes('\n')
			if len(bytes.TrimSpace(b)) > 0 {
				lines <- b
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case b, ok := <-lines:
			if !ok {
				p.shutdown()
				return nil
			}
			if err := p.send(b); err != nil {
				p.shutdown()
				return err
			}
		case err := <-p.fatal:
			p.shutdown()
			return err
		}
	}
}

func (p *proxy) shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.conn != nil {
		p.conn.Close()
	}
}

func (p *proxy) writeOut(b []byte) {
	p.outM.Lock()
	defer p.outM.Unlock()
	_, _ = p.out.Write(b)
}

func (p *proxy) send(line []byte) error {
	if line[len(line)-1] != '\n' {
		line = append(append([]byte{}, line...), '\n')
	}
	env := parseEnvelope(line)
	p.mu.Lock()
	switch {
	case env.Method == "initialize":
		p.initLine = append([]byte{}, line...)
	case env.Method == "notifications/initialized":
		p.initializedLine = append([]byte{}, line...)
	}
	if env.Method != "" && len(env.ID) > 0 {
		p.pending[string(env.ID)] = env.ID
	}
	c, gen := p.conn, p.gen
	p.mu.Unlock()

	if _, err := c.Write(line); err == nil {
		return nil
	}
	if err := p.reconnect(gen); err != nil {
		return err
	}
	p.mu.Lock()
	c = p.conn
	// The request was recorded as pending before the failure, so reconnect
	// answered it with an error; resending would answer it twice.
	delete(p.pending, string(env.ID))
	p.mu.Unlock()
	_, err := c.Write(line)
	return err
}

func (p *proxy) readLoop(c net.Conn, gen int) {
	r := bufio.NewReaderSize(c, 1<<20)
	for {
		b, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(b)) > 0 {
			env := parseEnvelope(b)
			if env.Method == "" && len(env.ID) > 0 {
				p.mu.Lock()
				id := string(env.ID)
				swallowed := id == p.swallow
				if swallowed {
					p.swallow = ""
				}
				delete(p.pending, id)
				p.mu.Unlock()
				if swallowed {
					continue
				}
			}
			p.writeOut(b)
		}
		if err != nil {
			p.mu.Lock()
			closed := p.closed
			p.mu.Unlock()
			if closed {
				return
			}
			if rerr := p.reconnect(gen); rerr != nil {
				select {
				case p.fatal <- rerr:
				default:
				}
			}
			return
		}
	}
}

// reconnect replaces the session that failed (generation failedGen) with a
// new one. If another goroutine already did, it does nothing.
func (p *proxy) reconnect(failedGen int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("closed")
	}
	if p.gen != failedGen {
		return nil
	}
	p.conn.Close()

	for _, id := range p.pending {
		msg, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]any{"code": -32000, "message": "the codive server was restarted; please retry"},
		})
		p.writeOut(append(msg, '\n'))
	}
	p.pending = map[string]json.RawMessage{}

	var c net.Conn
	var err error
	for deadline := time.Now().Add(reconnectWindow); ; {
		if c, err = p.dial(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}
	if p.initLine != nil {
		p.swallow = string(parseEnvelope(p.initLine).ID)
		if _, err := c.Write(p.initLine); err != nil {
			c.Close()
			return err
		}
		if p.initializedLine != nil {
			if _, err := c.Write(p.initializedLine); err != nil {
				c.Close()
				return err
			}
		}
	}
	p.conn = c
	p.gen++
	go p.readLoop(c, p.gen)
	return nil
}
