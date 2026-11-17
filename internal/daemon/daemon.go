// Package daemon lets many `codive serve` shims share one background process.
//
// Every AI client starts its own stdio server, so a machine with several
// editors open ends up with a dozen processes syncing and writing the same
// SQLite file. Here the first shim starts one detached daemon per user; every
// shim then forwards its MCP stream to it over a loopback socket. The daemon
// is the only writer of each index.
//
// The socket is guarded by a random token kept in a file only the user can
// read, and a shim whose version differs from the daemon's replaces the
// daemon, so upgrading codive never leaves old code running.
package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	handshakeTimeout = 5 * time.Second
	dialTimeout      = 2 * time.Second
	// replyTimeout bounds how long a shim waits for the daemon to accept a
	// session. Opening a repository for the first time indexes it first.
	replyTimeout = 15 * time.Minute
	// startLockStale is how old a start lock must be to be taken for the
	// leftover of a daemon that died while starting.
	startLockStale = 30 * time.Second
	maxLine        = 64 << 10
)

// Dir returns the directory holding the daemon's endpoint file, lock and log.
// CODIVE_DAEMON_DIR overrides it (tests do).
func Dir() string {
	if d := os.Getenv("CODIVE_DAEMON_DIR"); d != "" {
		return d
	}
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		return filepath.Join(d, "codive")
	}
	return filepath.Join(os.TempDir(), "codive")
}

// Endpoint says where a running daemon listens and how to talk to it.
type Endpoint struct {
	Addr    string `json:"addr"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// Handshake is the first line a shim sends.
type Handshake struct {
	Token   string `json:"token"`
	Version string `json:"version"`
	// Root is the workspace directory the session is for.
	Root string `json:"root,omitempty"`
	// Cmd, if set, is a control request ("status" or "shutdown") instead of
	// an MCP session.
	Cmd string `json:"cmd,omitempty"`
}

// Reply answers a Handshake.
type Reply struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Version string `json:"version,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Info    string `json:"info,omitempty"`
}

func endpointPath() string { return filepath.Join(Dir(), "daemon.json") }

// ReadEndpoint returns the running daemon's endpoint, if one is recorded.
func ReadEndpoint() (Endpoint, error) {
	var e Endpoint
	b, err := os.ReadFile(endpointPath())
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return e, err
	}
	if e.Addr == "" || e.Token == "" {
		return e, errors.New("incomplete daemon endpoint file")
	}
	return e, nil
}

func writeEndpoint(e Endpoint) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(e)
	tmp := endpointPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, endpointPath())
}

// RemoveEndpoint deletes the endpoint file if it still names pid, so a
// daemon shutting down can't erase its replacement's.
func RemoveEndpoint(pid int) {
	if e, err := ReadEndpoint(); err == nil && e.PID != pid {
		return
	}
	_ = os.Remove(endpointPath())
}

// Alive reports whether a daemon is accepting connections.
func Alive() bool {
	e, err := ReadEndpoint()
	if err != nil {
		return false
	}
	c, err := net.DialTimeout("tcp", e.Addr, dialTimeout)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// AcquireStartLock serializes daemon start-up: of several shims that find no
// daemon and each spawn one, only one gets to listen. The lock is held only
// until the endpoint is written.
func AcquireStartLock() (release func(), ok bool) {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return nil, false
	}
	path := filepath.Join(Dir(), "daemon.lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { _ = os.Remove(path) }, true
		}
		info, statErr := os.Stat(path)
		if statErr == nil && time.Since(info.ModTime()) < startLockStale {
			return nil, false
		}
		_ = os.Remove(path)
	}
	return nil, false
}

// Listener accepts shim connections on the loopback interface.
type Listener struct {
	ln net.Listener
	ep Endpoint
	// Session opens a session for root and returns the function that serves
	// it on the connection (called in its own goroutine, and responsible for
	// closing the connection). A non-nil error is sent to the shim.
	Session func(root string) (serve func(net.Conn), err error)
	// Control answers "status" and "shutdown" requests.
	Control func(cmd string) string
}

// Listen starts listening and records the endpoint for shims to find.
func Listen(version string) (*Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		ln.Close()
		return nil, err
	}
	ep := Endpoint{Addr: ln.Addr().String(), Token: hex.EncodeToString(tok), PID: os.Getpid(), Version: version}
	if err := writeEndpoint(ep); err != nil {
		ln.Close()
		return nil, fmt.Errorf("cannot record daemon endpoint: %w", err)
	}
	return &Listener{ln: ln, ep: ep}, nil
}

// Endpoint returns the listener's endpoint.
func (l *Listener) Endpoint() Endpoint { return l.ep }

// Close stops listening and removes the endpoint file.
func (l *Listener) Close() {
	_ = l.ln.Close()
	RemoveEndpoint(l.ep.PID)
}

// Serve accepts connections until the listener is closed.
func (l *Listener) Serve() error {
	for {
		c, err := l.ln.Accept()
		if err != nil {
			return err
		}
		go l.handshake(c)
	}
}

func (l *Listener) handshake(c net.Conn) {
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	line, err := readLine(c)
	if err != nil {
		c.Close()
		return
	}
	var hs Handshake
	if json.Unmarshal(line, &hs) != nil || subtle.ConstantTimeCompare([]byte(hs.Token), []byte(l.ep.Token)) != 1 {
		c.Close()
		return
	}
	reply := func(r Reply) bool {
		r.Version, r.PID = l.ep.Version, l.ep.PID
		b, _ := json.Marshal(r)
		_ = c.SetWriteDeadline(time.Now().Add(handshakeTimeout))
		_, err := c.Write(append(b, '\n'))
		return err == nil
	}
	if hs.Version != l.ep.Version {
		reply(Reply{Error: "version mismatch"})
		c.Close()
		return
	}
	if hs.Cmd != "" {
		info := ""
		if l.Control != nil {
			info = l.Control(hs.Cmd)
		}
		reply(Reply{OK: true, Info: info})
		c.Close()
		return
	}
	serve, err := l.Session(hs.Root)
	if err != nil {
		reply(Reply{Error: err.Error()})
		c.Close()
		return
	}
	if !reply(Reply{OK: true}) {
		// The shim is gone. Serving a closed connection ends at once and lets
		// the session release what opening it took.
		c.Close()
		serve(c)
		return
	}
	_ = c.SetDeadline(time.Time{})
	serve(c)
}

// readLine reads one line a byte at a time, so nothing after it is consumed
// and the caller can keep using the connection.
func readLine(c net.Conn) ([]byte, error) {
	var line []byte
	buf := make([]byte, 1)
	for len(line) < maxLine {
		if _, err := c.Read(buf); err != nil {
			return nil, err
		}
		if buf[0] == '\n' {
			return line, nil
		}
		line = append(line, buf[0])
	}
	return nil, errors.New("handshake line too long")
}

// MismatchError means a daemon of a different version is running.
type MismatchError struct {
	PID     int
	Version string
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("daemon %d runs codive %s", e.PID, e.Version)
}

func handshakeWith(hs Handshake, wait time.Duration) (net.Conn, Reply, error) {
	var r Reply
	e, err := ReadEndpoint()
	if err != nil {
		return nil, r, err
	}
	hs.Token = e.Token
	c, err := net.DialTimeout("tcp", e.Addr, dialTimeout)
	if err != nil {
		return nil, r, err
	}
	b, _ := json.Marshal(hs)
	_ = c.SetDeadline(time.Now().Add(dialTimeout))
	if _, err := c.Write(append(b, '\n')); err != nil {
		c.Close()
		return nil, r, err
	}
	_ = c.SetReadDeadline(time.Now().Add(wait))
	line, err := readLine(c)
	if err != nil {
		c.Close()
		return nil, r, err
	}
	if err := json.Unmarshal(line, &r); err != nil {
		c.Close()
		return nil, r, err
	}
	_ = c.SetDeadline(time.Time{})
	if !r.OK {
		c.Close()
		if r.Error == "version mismatch" {
			return nil, r, &MismatchError{PID: r.PID, Version: r.Version}
		}
		return nil, r, errors.New(r.Error)
	}
	return c, r, nil
}

// Dial opens a session for root on the running daemon.
func Dial(root, version string) (net.Conn, error) {
	c, _, err := handshakeWith(Handshake{Version: version, Root: root}, replyTimeout)
	return c, err
}

// Control sends a control request ("status" or "shutdown") to the daemon.
func Control(cmd, version string) (Reply, error) {
	c, r, err := handshakeWith(Handshake{Version: version, Cmd: cmd}, dialTimeout)
	if c != nil {
		c.Close()
	}
	return r, err
}

// Stop ends the daemon whatever its version, by process id.
func Stop() error {
	e, err := ReadEndpoint()
	if err != nil {
		return err
	}
	return stopPID(e)
}

func stopPID(e Endpoint) error {
	p, err := os.FindProcess(e.PID)
	if err != nil {
		return err
	}
	err = p.Kill()
	RemoveEndpoint(e.PID)
	return err
}

// EnsureAndDial opens a session, starting the daemon (or replacing one of
// another version) if needed. spawn starts a detached daemon process.
func EnsureAndDial(root, version string, spawn func() error) (net.Conn, error) {
	deadline := time.Now().Add(20 * time.Second)
	spawned := false
	for {
		c, err := Dial(root, version)
		if err == nil {
			return c, nil
		}
		var mm *MismatchError
		if errors.As(err, &mm) {
			// An older (or newer) daemon: replace it.
			if e, rerr := ReadEndpoint(); rerr == nil {
				_ = stopPID(e)
			}
			spawned = false
		} else if !isNoDaemon(err) {
			// The daemon answered and refused (for example, the repository
			// couldn't be opened): that is the answer, not a reason to retry.
			return nil, err
		}
		if !spawned {
			if serr := spawn(); serr != nil {
				return nil, fmt.Errorf("cannot start the codive daemon: %w", serr)
			}
			spawned = true
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the codive daemon did not come up: %w", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// isNoDaemon reports whether err means no daemon is reachable, as opposed to
// one that is running and said no.
func isNoDaemon(err error) bool {
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) || errors.Is(err, io.EOF)
}

