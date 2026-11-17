// Command agentbench measures whether codive helps a real coding agent finish
// real tasks, which output-size benchmarks cannot show.
//
// For every task it runs the same prompt twice per repeat, on identical fresh
// checkouts: once with Claude Code's built-in tools only ("builtin") and once
// with the codive MCP server added ("codive"). It records turns, tokens, cost,
// wall time and whether the task actually succeeded, then prints medians.
//
// It spends real API money. Nothing runs unless you pass -run.
//
//	go run ./scripts/agentbench -tasks scripts/agentbench/tasks.json -codive path/to/codive.exe -run
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Task struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"` // locate | change | review: for reporting only
	BaseCommit string   `json:"base_commit"`
	Setup      string   `json:"setup"`  // shell run in the checkout before the agent (e.g. apply a patch to review)
	Prompt     string   `json:"prompt"` // exactly what the agent is asked
	Verify     string   `json:"verify"` // shell command; exit 0 means the work is correct
	Expect     []string `json:"expect"` // substrings the final answer must all contain (case-insensitive)
}

type TaskFile struct {
	Repo  string `json:"repo"`  // path to the git repository under test
	Model string `json:"model"` // same model for both arms
	Tasks []Task `json:"tasks"`
}

type Run struct {
	Task      string  `json:"task"`
	Kind      string  `json:"kind"`
	Arm       string  `json:"arm"`
	Repeat    int     `json:"repeat"`
	Success   bool    `json:"success"`
	AgentErr  bool    `json:"agent_error"`
	Turns     int     `json:"turns"`
	Tokens    int     `json:"tokens"` // input + cache + output, as billed
	CostUSD   float64 `json:"cost_usd"`
	WallMS    int64   `json:"wall_ms"`
	Answer    string  `json:"answer"`
	VerifyOut string  `json:"verify_output,omitempty"`
}

// claudeResult is the subset of `claude -p --output-format json` we rely on.
// The field names are checked on the first run (see the "raw" files written
// next to the results); a missing field shows up as zeros, not a crash.
type claudeResult struct {
	IsError   bool    `json:"is_error"`
	NumTurns  int     `json:"num_turns"`
	Result    string  `json:"result"`
	CostUSD   float64 `json:"total_cost_usd"`
	DurationM int64   `json:"duration_ms"`
	Usage     struct {
		In       int `json:"input_tokens"`
		Out      int `json:"output_tokens"`
		CacheNew int `json:"cache_creation_input_tokens"`
		CacheHit int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func main() {
	tasksPath := flag.String("tasks", "scripts/agentbench/tasks.json", "task file")
	codive := flag.String("codive", "codive", "path to the codive binary")
	repeats := flag.Int("repeats", 3, "runs per task per arm (use at least 3; results vary run to run)")
	budget := flag.Float64("budget", 2.0, "max USD one agent run may spend")
	out := flag.String("out", "docs/agent_benchmark_results.json", "where to write every run")
	run := flag.Bool("run", false, "actually run agents (costs money); without it, only print the plan")
	flag.Parse()

	var tf TaskFile
	data, err := os.ReadFile(*tasksPath)
	must(err)
	must(json.Unmarshal(data, &tf))
	if len(tf.Tasks) == 0 {
		must(fmt.Errorf("no tasks in %s", *tasksPath))
	}

	total := len(tf.Tasks) * 2 * *repeats
	fmt.Printf("Plan: %d tasks x 2 arms x %d repeats = %d agent runs, up to $%.2f each (worst case $%.2f).\n",
		len(tf.Tasks), *repeats, total, *budget, float64(total)**budget)
	if !*run {
		fmt.Println("Dry run. Pass -run to execute.")
		return
	}

	var results []Run
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for _, t := range tf.Tasks {
		for r := 1; r <= *repeats; r++ {
			// Alternate order at random so neither arm always gets a warm cache.
			arms := []string{"builtin", "codive"}
			rng.Shuffle(2, func(i, j int) { arms[i], arms[j] = arms[j], arms[i] })
			for _, arm := range arms {
				fmt.Printf("• %s  %-8s  repeat %d ...", t.ID, arm, r)
				res := runOne(tf, t, arm, r, *codive, *budget)
				fmt.Printf(" success=%v turns=%d tokens=%d $%.3f %.0fs\n", res.Success, res.Turns, res.Tokens, res.CostUSD, float64(res.WallMS)/1000)
				results = append(results, res)
				b, _ := json.MarshalIndent(results, "", "  ")
				_ = os.WriteFile(*out, b, 0o644) // written after every run so a crash loses nothing
			}
		}
	}
	summarize(results)
}

func runOne(tf TaskFile, t Task, arm string, repeat int, codivePath string, budget float64) Run {
	res := Run{Task: t.ID, Kind: t.Kind, Arm: arm, Repeat: repeat}
	dir, err := os.MkdirTemp("", "agentbench-")
	must(err)
	defer func() {
		_ = sh(tf.Repo, "git worktree remove --force "+quote(dir))
		_ = os.RemoveAll(dir)
	}()
	must(sh(tf.Repo, "git worktree add --detach "+quote(dir)+" "+t.BaseCommit))

	// Both arms get the same files. Project instruction files are removed so
	// neither arm is told how to behave, and the codive index is excluded from
	// the builtin arm.
	for _, f := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", ".gemini", ".claude", ".codive"} {
		_ = os.RemoveAll(filepath.Join(dir, f))
	}
	if t.Setup != "" {
		must(sh(dir, t.Setup))
	}

	args := []string{"-p", t.Prompt, "--output-format", "json", "--permission-mode", "acceptEdits",
		"--setting-sources", "project", "--no-session-persistence",
		"--max-budget-usd", fmt.Sprintf("%.2f", budget), "--strict-mcp-config"}
	if tf.Model != "" {
		args = append(args, "--model", tf.Model)
	}
	if arm == "codive" {
		// Indexing is a one-off setup cost, reported separately by the caller; the
		// agent's cost starts after it, the way a user who already ran `codive init` sees it.
		must(sh(dir, quote(codivePath)+" init ."))
		cfg := filepath.Join(dir, ".agentbench-mcp.json")
		entry := map[string]any{"mcpServers": map[string]any{"codive": map[string]any{"command": codivePath, "args": []string{"serve", dir}}}}
		b, _ := json.Marshal(entry)
		must(os.WriteFile(cfg, b, 0o644))
		args = append(args, "--mcp-config", cfg)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	start := time.Now()
	runErr := cmd.Run()
	res.WallMS = time.Since(start).Milliseconds()
	_ = os.WriteFile(filepath.Join(os.TempDir(), fmt.Sprintf("agentbench-raw-%s-%s-%d.json", t.ID, arm, repeat)), stdout.Bytes(), 0o644)

	var cr claudeResult
	if err := json.Unmarshal(stdout.Bytes(), &cr); err != nil || runErr != nil {
		res.AgentErr = true
		return res
	}
	res.AgentErr = cr.IsError
	res.Turns = cr.NumTurns
	res.CostUSD = cr.CostUSD
	res.Tokens = cr.Usage.In + cr.Usage.Out + cr.Usage.CacheNew + cr.Usage.CacheHit
	res.Answer = cr.Result

	ok := !cr.IsError
	for _, want := range t.Expect {
		if !strings.Contains(strings.ToLower(cr.Result), strings.ToLower(want)) {
			ok = false
		}
	}
	if t.Verify != "" {
		cmd := exec.Command("bash", "-c", t.Verify)
		cmd.Dir = dir
		o, err := cmd.CombinedOutput()
		res.VerifyOut = tail(string(o), 1500)
		if err != nil {
			ok = false
		}
	}
	res.Success = ok
	return res
}

func summarize(rs []Run) {
	type key struct{ task, arm string }
	by := map[key][]Run{}
	var tasks []string
	seen := map[string]bool{}
	for _, r := range rs {
		by[key{r.Task, r.Arm}] = append(by[key{r.Task, r.Arm}], r)
		if !seen[r.Task] {
			seen[r.Task] = true
			tasks = append(tasks, r.Task)
		}
	}
	fmt.Println("\n| Task | Arm | Success | Turns (median) | Tokens (median) | Cost (median) | Time (median) |")
	fmt.Println("|:--|:--|:--|--:|--:|--:|--:|")
	for _, t := range tasks {
		for _, arm := range []string{"builtin", "codive"} {
			g := by[key{t, arm}]
			ok := 0
			var turns, toks, ms []float64
			var cost []float64
			for _, r := range g {
				if r.Success {
					ok++
				}
				turns = append(turns, float64(r.Turns))
				toks = append(toks, float64(r.Tokens))
				cost = append(cost, r.CostUSD)
				ms = append(ms, float64(r.WallMS))
			}
			fmt.Printf("| %s | %s | %d/%d | %.0f | %.0f | $%.3f | %.0fs |\n", t, arm, ok, len(g), median(turns), median(toks), median(cost), median(ms)/1000)
		}
	}
	fmt.Println("\nRead this honestly: a cheaper run that failed the task is not a saving. Compare cost only where success rates are equal, and treat differences smaller than the spread between repeats as noise.")
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func sh(dir, command string) error {
	cmd := exec.Command("bash", "-c", command)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w\n%s", command, err, tail(string(out), 800))
	}
	return nil
}

func quote(s string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(s), "'", `'\''`) + "'" }

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentbench:", err)
		os.Exit(1)
	}
}
