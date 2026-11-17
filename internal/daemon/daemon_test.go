package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// listen starts a listener whose sessions run serve, with its files in a
// temporary directory.
func listen(t *testing.T, version string, session func(root string) (func(net.Conn), error)) *Listener {
	t.Helper()
	t.Setenv("CODIVE_DAEMON_DIR", t.TempDir())
	ln, err := Listen(version)
	if err != nil {
		t.Fatal(err)
	}
	ln.Session = session
	go ln.Serve()
	t.Cleanup(ln.Close)
	return ln
}

// echo answers every line with "echo:" + the line.
func echo(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		c.Write([]byte("echo:" + line))
	}
}

func readReply(t *testing.T, c net.Conn) string {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return strings.TrimSpace(line)
}

func TestSessionCarriesTheStreamAfterTheHandshake(t *testing.T) {
	var gotRoot string
	listen(t, "v1", func(root string) (func(net.Conn), error) {
		gotRoot = root
		return echo, nil
	})
	c, err := Dial("/work/repo", "v1")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("hello\n"))
	if got := readReply(t, c); got != "echo:hello" {
		t.Errorf("got %q", got)
	}
	if gotRoot != "/work/repo" {
		t.Errorf("session opened for %q", gotRoot)
	}
}

func TestADaemonOfAnotherVersionIsReported(t *testing.T) {
	listen(t, "v1", func(string) (func(net.Conn), error) { return echo, nil })
	_, err := Dial("/r", "v2")
	var mm *MismatchError
	if !errors.As(err, &mm) || mm.Version != "v1" {
		t.Fatalf("Dial with another version: %v, want a MismatchError naming v1", err)
	}
}

func TestAConnectionWithoutTheTokenGetsNothing(t *testing.T) {
	ln := listen(t, "v1", func(string) (func(net.Conn), error) { return echo, nil })
	c, err := net.Dial("tcp", ln.Endpoint().Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, _ := json.Marshal(Handshake{Token: "wrong", Version: "v1", Root: "/r"})
	c.Write(append(b, '\n'))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := c.Read(make([]byte, 16)); err == nil || n > 0 {
		t.Errorf("a bad token was answered (n=%d, err=%v); it must be dropped", n, err)
	}
}

func TestARefusedSessionIsNotRetriedAsAMissingDaemon(t *testing.T) {
	listen(t, "v1", func(string) (func(net.Conn), error) { return nil, errors.New("cannot open /r") })
	spawned := 0
	_, err := EnsureAndDial("/r", "v1", func() error { spawned++; return nil })
	if err == nil || !strings.Contains(err.Error(), "cannot open /r") {
		t.Fatalf("EnsureAndDial: %v, want the daemon's reason", err)
	}
	if spawned != 0 {
		t.Errorf("spawned %d daemons although one answered", spawned)
	}
}

func TestEnsureAndDialStartsADaemonWhenThereIsNone(t *testing.T) {
	t.Setenv("CODIVE_DAEMON_DIR", t.TempDir())
	var ln *Listener
	t.Cleanup(func() {
		if ln != nil {
			ln.Close()
		}
	})
	spawn := func() error {
		l, err := Listen("v1")
		if err != nil {
			return err
		}
		l.Session = func(string) (func(net.Conn), error) { return echo, nil }
		ln = l
		go l.Serve()
		return nil
	}
	c, err := EnsureAndDial("/r", "v1", spawn)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestStartLockAllowsOneStarter(t *testing.T) {
	t.Setenv("CODIVE_DAEMON_DIR", t.TempDir())
	release, ok := AcquireStartLock()
	if !ok {
		t.Fatal("first starter refused")
	}
	if _, ok2 := AcquireStartLock(); ok2 {
		t.Error("a second starter got the lock while the first holds it")
	}
	release()
	if _, ok3 := AcquireStartLock(); !ok3 {
		t.Error("the lock was not released")
	}
}

// syncBuf is a writer the test can wait on.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuf) waitFor(t *testing.T, sub string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(s.String(), sub) {
			return
		}
	}
	t.Fatalf("output never contained %q; got:\n%s", sub, s.String())
}

// When the daemon goes away mid-session, the shim opens a new session and
// replays the client's initialize handshake, answers the request that was in
// flight with an error, and keeps working. The client never sees a second
// initialize reply.
func TestProxySurvivesTheDaemonDroppingTheSession(t *testing.T) {
	var mu sync.Mutex
	sessions := 0
	secondSession := make(chan []string, 1)

	reply := func(c net.Conn, id json.RawMessage, result string) {
		c.Write([]byte(`{"jsonrpc":"2.0","id":` + string(id) + `,"result":"` + result + `"}` + "\n"))
	}
	listen(t, "v1", func(string) (func(net.Conn), error) {
		mu.Lock()
		sessions++
		n := sessions
		mu.Unlock()
		return func(c net.Conn) {
			defer c.Close()
			var seen []string
			r := bufio.NewReader(c)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				var m envelope
				_ = json.Unmarshal([]byte(line), &m)
				seen = append(seen, m.Method)
				switch {
				case m.Method == "initialize":
					reply(c, m.ID, "init-from-session-"+string(rune('0'+n)))
				case m.Method == "crash" && n == 1:
					return // the daemon dies without answering
				case m.Method == "ping":
					reply(c, m.ID, "pong-from-session-"+string(rune('0'+n)))
					if n == 2 {
						select {
						case secondSession <- seen:
						default:
						}
					}
				}
			}
		}, nil
	})

	in, inW := io.Pipe()
	out := &syncBuf{}
	done := make(chan error, 1)
	go func() { done <- Proxy(in, out, func() (net.Conn, error) { return Dial("/r", "v1") }) }()

	send := func(s string) { inW.Write([]byte(s + "\n")) }
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	out.waitFor(t, "init-from-session-1")
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"crash"}`)
	out.waitFor(t, `"id":2`)
	out.waitFor(t, "restarted")
	send(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	out.waitFor(t, "pong-from-session-2")

	if n := strings.Count(out.String(), "init-from-session"); n != 1 {
		t.Errorf("the client saw %d initialize replies, want exactly 1:\n%s", n, out.String())
	}
	select {
	case seen := <-secondSession:
		if len(seen) < 3 || seen[0] != "initialize" || seen[1] != "notifications/initialized" || seen[2] != "ping" {
			t.Errorf("the new session received %v, want initialize, notifications/initialized, ping", seen)
		}
	case <-time.After(5 * time.Second):
		t.Error("the second session never answered a ping")
	}

	inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Proxy returned %v after the client closed its input", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Proxy did not return when the client closed its input")
	}
}
