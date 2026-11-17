# Agent benchmark

Answers one question the output-size benchmark cannot: **does codive make a real coding agent finish real tasks with fewer turns, tokens, cost or time, without finishing fewer of them?**

## How it works

For each task and each repeat it runs the same prompt twice, on identical fresh `git worktree` checkouts, in random order:

| Arm | Setup |
|:--|:--|
| `builtin` | Claude Code with its normal tools only (`--strict-mcp-config`, no servers). |
| `codive` | Same, plus the codive MCP server registered as `codive serve <project>`, the way `codive setup` does it. `codive init` runs first and is **not** counted in the agent's cost. |

Fairness rules, all enforced by the runner:
- The same model and prompt for both arms. Pin `model` in `tasks.json`.
- `CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `.claude`, `.gemini` and `.codive` are deleted from the checkout, so neither arm is told how to behave. Only `--setting-sources project` is read.
- Arm order is shuffled per repeat, so neither arm always gets a warm cache.
- Success is checked outside the agent: the final answer must contain every `expect` string and the `verify` command (if any) must exit 0.

Recorded per run: success, turns, tokens (input + cache + output), cost in USD, wall time. Every run is appended to `docs/agent_benchmark_results.json`, and the raw `claude` JSON goes to your temp directory so you can check the field names on the first run.

## Running it

```
go build -o codive.exe ./...            # or point -codive at an installed binary
go run ./scripts/agentbench             # dry run: prints the plan and worst-case spend
go run ./scripts/agentbench -run -codive ./codive.exe -repeats 3
```

It spends real money: each run is capped by `-budget` (default $2). Start with one task and `-repeats 1` to confirm the output parses.

## Reading the results

- A cheaper run that failed is not a saving. Compare cost only where success rates match.
- Differences smaller than the spread between repeats are noise. Use at least 3 repeats, ideally 5.
- Results hold for the tasks, repository, model and version you ran. Say that when you quote them.
- Do not publish a number until the results file is committed beside it.

## Adding tasks

Add entries to `tasks.json`. Good tasks have an answer you can check mechanically: a known set of callers, a failing test the agent must make pass (`verify`), a change you can grep for. Cover the three claims worth testing: finding things in a big repo (`locate`), understanding a diff (`review`) and making a safe change (`change`). Add at least one task on a large, non-Go repository, because that is where Go-only call tracing will show up as a weakness.
