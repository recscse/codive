package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/recscse/codive/internal/daemon"
	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/indexer"
	"github.com/recscse/codive/internal/ui"
	"github.com/recscse/codive/internal/workspace"
)

const (
	// largeIndexBytes is the index size above which doctor suggests trimming it.
	largeIndexBytes = 1 << 30
	// logTailLines is how much of each log doctor reads when counting failures.
	logTailLines = 200
)

// doctorIndexChecks reports the problems that made codive slow or wrong on
// large repositories: two indexes for one repository, an index built by an
// older extractor, an index too big to answer quickly, sync failures in the
// log, and a daemon that doesn't match this binary.
func doctorIndexChecks(absDir, version string) {
	root := workspace.Resolve(absDir)
	if root != absDir {
		ui.CheckWarn(fmt.Sprintf("%s is inside the repository indexed at %s; that index serves it", absDir, root))
	}

	for _, nested := range workspace.NestedIndexes(root, 4) {
		ui.CheckWarn(fmt.Sprintf("A second index at %s duplicates part of the one at %s; delete its .codive folder so one index serves the repository", nested, root))
	}

	dbPath := filepath.Join(root, workspace.IndexDir, "index.db")
	if info, err := os.Stat(dbPath); err == nil {
		if info.Size() > largeIndexBytes {
			ui.CheckWarn(fmt.Sprintf("The index is %s. Large generated or non-code files are the usual cause; list them in .codiveignore, then run 'codive init'", formatBytes(info.Size())))
		}
		if database, err := db.OpenReadOnly(dbPath); err == nil {
			if indexer.NeedsReextract(context.Background(), database) {
				ui.CheckWarn("The index was built by an older symbol extractor; searches may miss symbols until it is rebuilt (run 'codive update', or restart your AI client)")
			}
			database.Close()
		}
	}

	for _, log := range []string{
		filepath.Join(root, workspace.IndexDir, "codive.log"),
		filepath.Join(daemon.Dir(), "daemon.log"),
	} {
		if n, last := recentSyncFailures(log); n > 0 {
			ui.CheckWarn(fmt.Sprintf("%d of the last %d lines of %s are index sync failures; latest: %s", n, logTailLines, log, last))
		}
	}

	if ep, err := daemon.ReadEndpoint(); err == nil && daemon.Alive() {
		if ep.Version != version {
			ui.CheckWarn(fmt.Sprintf("The shared daemon runs codive %s but this is %s; it is replaced the next time an AI client connects, or run 'codive daemon stop'", ep.Version, version))
		} else {
			ui.CheckPass(fmt.Sprintf("Shared daemon running  (pid %d, codive %s)", ep.PID, ep.Version))
		}
	} else {
		ui.CheckPass("Shared daemon not running  (it starts by itself when an AI client connects)")
	}
}

// recentSyncFailures counts the sync failures among the last logTailLines
// lines of a log file and returns the latest one's error text.
func recentSyncFailures(path string) (count int, latest string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > logTailLines {
		lines = lines[len(lines)-logTailLines:]
	}
	for _, l := range lines {
		if !strings.Contains(l, "auto-sync failed") {
			continue
		}
		count++
		latest = l
		if i := strings.Index(l, `"error":"`); i >= 0 {
			latest = strings.TrimRight(l[i+len(`"error":"`):], `"}`)
		}
	}
	return count, latest
}
