package mcp

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/symbols"
)

// The intent benchmark turns a repository's history into test questions:
// each commit's subject is a query, and the definitions that commit changed
// (and that still exist) are its answers. Nobody hand-picks the questions,
// so the result isn't tuned to what the search happens to be good at.

// maxBenchAnswers skips commits that changed more definitions than this:
// broad refactors don't have one place the subject describes.
const maxBenchAnswers = 8

// conventionalPrefix matches "fix(mcp): " and similar commit prefixes.
var conventionalPrefix = regexp.MustCompile(`^([a-zA-Z]+)(\([^)]*\))?!?:\s*`)

// skippedCommitTypes are conventional commit types that don't describe code.
var skippedCommitTypes = map[string]bool{
	"docs": true, "chore": true, "ci": true, "build": true, "style": true, "release": true, "test": true,
}

// IntentBenchCase is one commit used as a question.
type IntentBenchCase struct {
	Commit  string   `json:"commit"`
	Query   string   `json:"query"`
	Answers []string `json:"answers"` // path:Name
	// SymbolRank and FileRank are the 1-based ranks of the first answer
	// (0 if it wasn't in the results); KeywordFileRank is the grep-style
	// baseline's rank of the first answer's file.
	SymbolRank      int           `json:"symbol_rank"`
	FileRank        int           `json:"file_rank"`
	KeywordFileRank int           `json:"keyword_file_rank"`
	Latency         time.Duration `json:"latency_ns"`
	Top             []string      `json:"top3"`
}

// IntentBenchReport summarizes a benchmark run.
type IntentBenchReport struct {
	CommitsScanned int               `json:"commits_scanned"`
	Skipped        map[string]int    `json:"skipped"`
	Cases          []IntentBenchCase `json:"cases"`
	Symbol         RankStats         `json:"symbol"`
	File           RankStats         `json:"file"`
	KeywordFile    RankStats         `json:"keyword_file_baseline"`
	MedianLatency  time.Duration     `json:"median_latency_ns"`
	ModelVersion   int               `json:"model_version"`
}

// RankStats are hit rates at 1, 3 and 10, and mean reciprocal rank.
type RankStats struct {
	Hit1  float64 `json:"hit_at_1"`
	Hit3  float64 `json:"hit_at_3"`
	Hit10 float64 `json:"hit_at_10"`
	MRR   float64 `json:"mrr"`
}

func rankStats(ranks []int) RankStats {
	var s RankStats
	if len(ranks) == 0 {
		return s
	}
	for _, r := range ranks {
		if r == 0 {
			continue
		}
		if r <= 1 {
			s.Hit1++
		}
		if r <= 3 {
			s.Hit3++
		}
		if r <= 10 {
			s.Hit10++
		}
		s.MRR += 1 / float64(r)
	}
	n := float64(len(ranks))
	s.Hit1, s.Hit3, s.Hit10, s.MRR = s.Hit1/n, s.Hit3/n, s.Hit10/n, s.MRR/n
	return s
}

// RunIntentBenchmark scores intent search on up to maxCommits recent
// non-merge commits of the repository at dir, whose index is database. It
// uses the ranker currently stored for the workspace (cold start if none)
// and logs nothing, so running it doesn't feed the learner.
func RunIntentBenchmark(ctx context.Context, database *sql.DB, dir string, maxCommits int) (*IntentBenchReport, error) {
	// One git log for every commit's diff, and one cat-file process for every
	// old file version: starting git per commit made a run take minutes.
	out, err := gitOutput(ctx, dir, "log", "--no-merges", "-n", strconv.Itoa(maxCommits),
		"-p", "-U0", "--no-color", "--no-renames", "--format=%x1e%H%x1f%P%x1f%s")
	if err != nil {
		return nil, err
	}
	blobs, err := newBlobReader(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer blobs.close()
	model := loadRanker(ctx, database)
	rep := &IntentBenchReport{Skipped: make(map[string]int), ModelVersion: model.Version}
	current := newFileCache(dir)
	keyword, err := newKeywordBaseline(ctx, database)
	if err != nil {
		return nil, err
	}

	var symRanks, fileRanks, kwRanks []int
	var latencies []time.Duration
	for _, record := range strings.Split(string(out), "\x1e")[1:] {
		header, diff, _ := strings.Cut(record, "\n")
		fields := strings.SplitN(header, "\x1f", 3)
		if len(fields) != 3 {
			continue
		}
		hash, parents, subject := fields[0], fields[1], fields[2]
		rep.CommitsScanned++
		if parents == "" {
			rep.Skipped["root commit (no diff to learn from)"]++
			continue
		}
		query, reason := benchQuery(subject)
		if reason != "" {
			rep.Skipped[reason]++
			continue
		}
		answers := changedDefinitions(hash, []byte(diff), current, blobs)
		if len(answers) == 0 {
			rep.Skipped["changed no definition that still exists"]++
			continue
		}
		if len(answers) > maxBenchAnswers {
			rep.Skipped[fmt.Sprintf("changed more than %d definitions", maxBenchAnswers)]++
			continue
		}

		terms := symbols.QueryTerms(query)
		start := time.Now()
		ranked, err := rankIntent(ctx, database, terms, "", model)
		elapsed := time.Since(start)
		if err != nil {
			return nil, err
		}
		c := IntentBenchCase{Commit: hash[:min(8, len(hash))], Query: query, Latency: elapsed}
		answerFiles := make(map[string]bool)
		for a := range answers {
			c.Answers = append(c.Answers, a)
			answerFiles[a[:strings.LastIndex(a, ":")]] = true
		}
		sort.Strings(c.Answers)

		var files []string
		seenFile := make(map[string]bool)
		for i, r := range ranked {
			key := r.FilePath + ":" + r.Name
			if i < 3 {
				c.Top = append(c.Top, key)
			}
			if c.SymbolRank == 0 && answers[key] {
				c.SymbolRank = i + 1
			}
			if !seenFile[r.FilePath] {
				seenFile[r.FilePath] = true
				files = append(files, r.FilePath)
				if c.FileRank == 0 && answerFiles[r.FilePath] {
					c.FileRank = len(files)
				}
			}
		}
		c.KeywordFileRank = keyword.rank(terms, answerFiles)

		rep.Cases = append(rep.Cases, c)
		symRanks = append(symRanks, c.SymbolRank)
		fileRanks = append(fileRanks, c.FileRank)
		kwRanks = append(kwRanks, c.KeywordFileRank)
		latencies = append(latencies, elapsed)
	}
	rep.Symbol, rep.File, rep.KeywordFile = rankStats(symRanks), rankStats(fileRanks), rankStats(kwRanks)
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		rep.MedianLatency = latencies[len(latencies)/2]
	}
	return rep, nil
}

// benchQuery turns a commit subject into a query, or says why it's skipped.
func benchQuery(subject string) (query, skip string) {
	s := strings.TrimSpace(subject)
	if strings.HasPrefix(s, "Merge ") || strings.HasPrefix(s, "Revert ") {
		return "", "merge or revert"
	}
	if m := conventionalPrefix.FindStringSubmatch(s); m != nil {
		if skippedCommitTypes[strings.ToLower(m[1])] {
			return "", "docs/chore/test/release commit"
		}
		s = s[len(m[0]):]
	}
	if len(symbols.QueryTerms(s)) < 2 {
		return "", "subject too short to search"
	}
	return s, ""
}

// changedDefinitions returns the definitions (path:Name) that commit hash
// changed and that still exist with the same name, kind, and file. A type
// is left out when a definition inside it changed, since the inner one is
// the precise answer.
func changedDefinitions(hash string, diff []byte, current *fileCache, blobs *blobReader) map[string]bool {
	answers := make(map[string]bool)
	for path, hunks := range parseNewSideHunks(diff) {
		now, err := current.get(path)
		if err != nil {
			continue // deleted since, or not readable
		}
		exists := make(map[string]bool)
		for _, s := range now.syms {
			exists[s.Name+"\x00"+s.Kind] = true
		}
		then, err := blobs.read(hash + ":" + path)
		if err != nil {
			continue
		}
		lang := scanner.DetectLanguage(path)
		syms, _ := symbols.ExtractSymbols(path, lang, then)
		type hit struct {
			sym        db.SymbolRecord
			start, end int
		}
		var hits []hit
		extents := symbols.Extents(lang, then, syms)
		for i, s := range syms {
			if !symbols.IsDefinition(s.Kind) {
				continue
			}
			start, end := extents[i][0], extents[i][1]
			if start < 1 {
				continue
			}
			for _, h := range hunks {
				if h[0] <= end && h[1] >= start {
					hits = append(hits, hit{s, start, end})
					break
				}
			}
		}
		for _, h := range hits {
			if typeDeclKinds[h.sym.Kind] {
				inner := false
				for _, o := range hits {
					if o.sym != h.sym && o.start >= h.start && o.end <= h.end && !typeDeclKinds[o.sym.Kind] {
						inner = true
						break
					}
				}
				if inner {
					continue
				}
			}
			if exists[h.sym.Name+"\x00"+h.sym.Kind] {
				answers[path+":"+h.sym.Name] = true
			}
		}
	}
	return answers
}

// blobReader reads file versions from one long-running `git cat-file --batch`.
type blobReader struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func newBlobReader(ctx context.Context, dir string) (*blobReader, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "cat-file", "--batch")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	return &blobReader{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20)}, nil
}

// read returns the content of object, such as "<commit>:<path>".
func (b *blobReader) read(object string) ([]byte, error) {
	if _, err := io.WriteString(b.in, object+"\n"); err != nil {
		return nil, err
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return nil, fmt.Errorf("git cat-file: %s", strings.TrimSpace(header))
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, err
	}
	data := make([]byte, size+1) // content plus its trailing newline
	if _, err := io.ReadFull(b.out, data); err != nil {
		return nil, err
	}
	if fields[1] != "blob" {
		return nil, fmt.Errorf("%s is a %s, not a file", object, fields[1])
	}
	return data[:size], nil
}

func (b *blobReader) close() {
	b.in.Close()
	_ = b.cmd.Wait()
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// parseNewSideHunks maps each file in a -U0 diff to the line ranges it
// changed in the new version. A pure deletion counts as the line it was at.
func parseNewSideHunks(diff []byte) map[string][][2]int {
	out := make(map[string][][2]int)
	path := ""
	sc := bufio.NewScanner(bytes.NewReader(diff))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "+++ "):
			path = ""
			if p := strings.TrimPrefix(l, "+++ "); strings.HasPrefix(p, "b/") {
				path = p[2:]
			}
		case path != "" && strings.HasPrefix(l, "@@ "):
			m := hunkHeader.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			end := start + count - 1
			if count == 0 {
				end = start
			}
			out[path] = append(out[path], [2]int{start, end})
		}
	}
	return out
}

func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}

// keywordBaseline ranks indexed files the way searching for the query's
// words would: by how many times they appear, case-insensitively.
type keywordBaseline struct {
	paths    []string
	contents []string
}

func newKeywordBaseline(ctx context.Context, database *sql.DB) (*keywordBaseline, error) {
	rows, err := database.QueryContext(ctx, "SELECT path, content FROM file_fts;")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kb := &keywordBaseline{}
	for rows.Next() {
		var p, c string
		if err := rows.Scan(&p, &c); err != nil {
			return nil, err
		}
		kb.paths = append(kb.paths, p)
		kb.contents = append(kb.contents, strings.ToLower(c))
	}
	return kb, rows.Err()
}

// rank returns the 1-based rank of the first file in want, or 0 if no file
// mentions any term.
func (kb *keywordBaseline) rank(terms []string, want map[string]bool) int {
	type scored struct {
		path  string
		count int
	}
	var all []scored
	for i, c := range kb.contents {
		n := 0
		for _, t := range terms {
			n += strings.Count(c, t)
		}
		if n > 0 {
			all = append(all, scored{kb.paths[i], n})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].count != all[j].count {
			return all[i].count > all[j].count
		}
		return all[i].path < all[j].path
	})
	for i, s := range all {
		if want[s.path] {
			return i + 1
		}
	}
	return 0
}
