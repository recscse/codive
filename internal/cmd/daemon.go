package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/recscse/codive/internal/daemon"
	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/graph"
	"github.com/recscse/codive/internal/indexer"
	"github.com/recscse/codive/internal/logger"
	"github.com/recscse/codive/internal/mcp"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/ui"
)

// readyWait is how long a tool call waits for a repository that is still
// being indexed before it answers that indexing is in progress.
var readyWait = 40 * time.Second

const (
	// repoIdleGrace is how long a repository stays open (and keeps being
	// watched) after its last client disconnects, so a client that reconnects
	// finds it warm.
	repoIdleGrace = 5 * time.Minute
	// daemonIdleExit is how long the daemon runs with no clients at all.
	daemonIdleExit = 10 * time.Minute
)

// repoEntry is one repository the daemon has open: its database, its sync
// loop, and how many client sessions use it.
type repoEntry struct {
	root   string
	db     *sql.DB
	fresh  *indexer.Freshener
	cancel context.CancelFunc
	refs   int
	idle   *time.Timer
	ready  chan struct{} // closed once opening finished; err is set before
	err    error
}

// host owns every index the daemon serves. It is the only writer of each, so
// clients never contend for the database.
type host struct {
	version string
	pool    *mcp.DBPool
	started time.Time

	// initIndex builds the index of a repository that has none (a test hook).
	initIndex func(root string) error
	// stop ends the daemon.
	stop func()

	mu         sync.Mutex
	repos      map[string]*repoEntry
	fresheners map[string]*indexer.Freshener
	conns      int
	lastActive time.Time
}

func newHost(version string) *host {
	return &host{
		version:    version,
		pool:       mcp.NewDBPool(),
		started:    time.Now(),
		initIndex:  RunInitSilent,
		repos:      map[string]*repoEntry{},
		fresheners: map[string]*indexer.Freshener{},
		lastActive: time.Now(),
	}
}

// syncLogger returns the OnSync callback for a repository's Freshener; the log
// of one daemon covers many repositories, so every line names its own.
func syncLogger(root string) func(*scanner.IncrementalResult, error) {
	l := slog.With("repo", root)
	return func(res *scanner.IncrementalResult, err error) { logSyncTo(l, res, err) }
}

// acquire counts a session on root, starting to open it if it isn't open yet.
// It returns at once: opening a repository for the first time builds its
// index, which takes minutes on a large one, and the client must not wait for
// that before its first reply. Tool calls wait on the entry (see ready).
func (h *host) acquire(root string) *repoEntry {
	h.mu.Lock()
	e := h.repos[root]
	creator := e == nil
	if creator {
		e = &repoEntry{root: root, ready: make(chan struct{})}
		h.repos[root] = e
	}
	e.refs++
	if e.idle != nil {
		e.idle.Stop()
		e.idle = nil
	}
	h.mu.Unlock()

	if creator {
		go h.open(e)
	}
	return e
}

// ready holds a tool call until the repository is open. A repository that is
// still being indexed after readyWait gets an answer that says so, so the
// client can try again, instead of a call that hangs until it times out.
func (h *host) ready(ctx context.Context, e *repoEntry) error {
	select {
	case <-e.ready:
		return e.err
	default:
	}
	t := time.NewTimer(readyWait)
	defer t.Stop()
	select {
	case <-e.ready:
		return e.err
	case <-t.C:
		return fmt.Errorf("The index for %s is still being built (this is its first use). Try again in a minute; the first index of a large repository can take several minutes.", e.root)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *host) open(e *repoEntry) {
	defer close(e.ready)
	dbPath := filepath.Join(e.root, ".codive", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// The first session on a repository builds its index. Stdout is not a
		// protocol stream here, but nothing reads it either.
		if err := h.initIndex(e.root); err != nil {
			h.fail(e, fmt.Errorf("indexing %s failed: %w", e.root, err))
			return
		}
	}
	database, err := db.Open(dbPath)
	if err != nil {
		h.fail(e, fmt.Errorf("failed to open the index of %s: %w", e.root, err))
		return
	}
	e.db = database
	h.pool.Put(e.root, database)

	fresh := indexer.NewFreshener(database, e.root)
	fresh.OnSync = syncLogger(e.root)
	ctx, cancel := context.WithCancel(context.Background())
	e.fresh, e.cancel = fresh, cancel
	h.mu.Lock()
	h.fresheners[filepath.Clean(e.root)] = fresh
	h.mu.Unlock()

	go func() {
		rebuild := func() {
			if rebuilt, err := indexer.RebuildIfOutdated(ctx, database, e.root); err != nil {
				slog.Warn("Re-indexing for the updated symbol extractor failed", "repo", e.root, "error", err)
			} else if rebuilt {
				slog.Info("Re-indexed for the updated symbol extractor", "repo", e.root)
			}
		}
		rebuild()
		// The code graph is what the graph tools read. It is built here, off
		// the request path, because a tool call has 60 seconds and building
		// it takes longer than that on a large repository.
		if err := graph.EnsureFresh(ctx, database, e.root); err != nil && ctx.Err() == nil {
			slog.Warn("Code graph build failed", "repo", e.root, "error", err)
		}
		startBackgroundWatcher(ctx, fresh, rebuild)
	}()
}

// fail records why e couldn't be opened and forgets it, so the next client to
// ask for the repository tries again. Sessions already waiting on e see err.
func (h *host) fail(e *repoEntry, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e.err = err
	if h.repos[e.root] == e {
		delete(h.repos, e.root)
	}
	slog.Error("Could not open repository", "repo", e.root, "error", err)
}

func (h *host) release(e *repoEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e.refs--
	if e.refs > 0 || e.err != nil {
		return
	}
	e.idle = time.AfterFunc(repoIdleGrace, func() { h.closeIfIdle(e) })
}

func (h *host) closeIfIdle(e *repoEntry) {
	h.mu.Lock()
	if e.refs > 0 || h.repos[e.root] != e {
		h.mu.Unlock()
		return
	}
	delete(h.repos, e.root)
	if h.fresheners[filepath.Clean(e.root)] == e.fresh {
		delete(h.fresheners, filepath.Clean(e.root))
	}
	h.mu.Unlock()
	h.closeEntry(e)
	slog.Info("Closed idle repository", "repo", e.root)
}

func (h *host) closeEntry(e *repoEntry) {
	if e.cancel != nil {
		e.cancel()
	}
	if e.db != nil {
		h.pool.Remove(e.root)
		_ = e.db.Close()
	}
}

func (h *host) closeAll() {
	h.mu.Lock()
	entries := make([]*repoEntry, 0, len(h.repos))
	for _, e := range h.repos {
		entries = append(entries, e)
	}
	h.repos = map[string]*repoEntry{}
	h.fresheners = map[string]*indexer.Freshener{}
	h.mu.Unlock()
	for _, e := range entries {
		h.closeEntry(e)
	}
}

// freshness brings the index of dir up to date for a tool call, waiting only a
// moment (see queryFreshnessWait).
func (h *host) freshness(ctx context.Context, dir string, database *sql.DB) {
	key := filepath.Clean(dir)
	h.mu.Lock()
	f, ok := h.fresheners[key]
	if !ok {
		f = indexer.NewFreshener(database, dir)
		f.OnSync = syncLogger(dir)
		h.fresheners[key] = f
	}
	h.mu.Unlock()
	waitForFreshness(ctx, f)
}

// session is daemon.Listener.Session: it opens the repository for a shim and
// returns the function that serves its MCP stream.
func (h *host) session(root string) (func(net.Conn), error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("workspace %q is not an absolute path", root)
	}
	root = filepath.Clean(root)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace %s is not an accessible directory", root)
	}
	e := h.acquire(root)
	h.mu.Lock()
	h.conns++
	h.mu.Unlock()

	return func(c net.Conn) {
		// e.db is not set until the repository is open, and the gate holds
		// every tool call until then, so the server needs no handle yet.
		srv := mcp.NewServer(e.root, nil, h.version)
		srv.SetReadyGate(func(ctx context.Context) error { return h.ready(ctx, e) })
		srv.SetDatabasePool(h.pool)
		srv.PinWorkspace()
		srv.SetFreshnessHook(h.freshness)
		_ = srv.Serve(c, c)
		srv.Close()
		c.Close()

		h.mu.Lock()
		h.conns--
		h.lastActive = time.Now()
		h.mu.Unlock()
		h.release(e)
	}, nil
}

// status describes what the daemon is doing, for `codive daemon status`.
func (h *host) status() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var sb strings.Builder
	fmt.Fprintf(&sb, "up %s, %d client session(s), %d repositor%s open\n",
		time.Since(h.started).Round(time.Second), h.conns, len(h.repos), map[bool]string{true: "y", false: "ies"}[len(h.repos) == 1])
	roots := make([]string, 0, len(h.repos))
	for r := range h.repos {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	for _, r := range roots {
		fmt.Fprintf(&sb, "  %s  (%d session(s))\n", r, h.repos[r].refs)
	}
	return sb.String()
}

// idleWatch stops the daemon once nothing has used it for daemonIdleExit.
func (h *host) idleWatch(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.mu.Lock()
			idle := h.conns == 0 && time.Since(h.lastActive) > daemonIdleExit
			h.mu.Unlock()
			if idle {
				slog.Info("No clients for a while; stopping")
				h.stop()
				return
			}
		}
	}
}

// RunDaemon runs the background process that every `codive serve` shim talks
// to. It exits quietly if one is already running.
func RunDaemon(version string) error {
	if err := os.MkdirAll(daemon.Dir(), 0o700); err == nil {
		if logger.InitAt(filepath.Join(daemon.Dir(), "daemon.log"), false) == nil {
			defer logger.Close()
		}
	}
	release, ok := daemon.AcquireStartLock()
	if !ok {
		return nil // another daemon is starting
	}
	if daemon.Alive() {
		release()
		return nil
	}
	ln, err := daemon.Listen(version)
	release()
	if err != nil {
		return err
	}

	h := newHost(version)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.stop = ln.Close
	ln.Session = h.session
	ln.Control = func(cmd string) string {
		switch cmd {
		case "status":
			return h.status()
		case "shutdown":
			go ln.Close()
			return "stopping"
		}
		return "unknown command"
	}
	go h.idleWatch(ctx)

	slog.Info("codive daemon started", "pid", os.Getpid(), "version", version, "addr", ln.Endpoint().Addr)
	_ = ln.Serve() // returns when the listener is closed
	h.closeAll()
	slog.Info("codive daemon stopped")
	return nil
}

// spawnDaemon starts the daemon as a detached background process.
func spawnDaemon() error {
	exe, err := daemon.Executable()
	if err != nil {
		return err
	}
	return daemon.SpawnDetached(exe, "daemon", "run")
}

// RunDaemonCommand implements `codive daemon [run|start|status|stop]`.
func RunDaemonCommand(args []string, version string) error {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "run":
		return RunDaemon(version)
	case "start":
		if daemon.Alive() {
			ui.Info("The daemon is already running.")
			return nil
		}
		if err := spawnDaemon(); err != nil {
			return err
		}
		for i := 0; i < 40; i++ {
			if daemon.Alive() {
				ui.Success("The daemon is running.")
				return nil
			}
			time.Sleep(150 * time.Millisecond)
		}
		return fmt.Errorf("the daemon did not start; see %s", filepath.Join(daemon.Dir(), "daemon.log"))
	case "stop":
		if !daemon.Alive() {
			ui.Info("The daemon is not running.")
			return nil
		}
		if err := daemon.Stop(); err != nil {
			return err
		}
		ui.Success("The daemon was stopped.")
		return nil
	case "status":
		ep, err := daemon.ReadEndpoint()
		if err != nil || !daemon.Alive() {
			ui.Info("The daemon is not running. It starts by itself when an AI client connects.")
			return nil
		}
		ui.KeyValue("Daemon", fmt.Sprintf("pid %d, codive %s, %s", ep.PID, ep.Version, ep.Addr))
		r, err := daemon.Control("status", ep.Version)
		if err != nil {
			return err
		}
		fmt.Print("  " + strings.ReplaceAll(strings.TrimRight(r.Info, "\n"), "\n", "\n  ") + "\n")
		ui.KeyValue("Log", filepath.Join(daemon.Dir(), "daemon.log"))
		return nil
	}
	return fmt.Errorf("unknown daemon command %q (use run, start, status or stop)", sub)
}
