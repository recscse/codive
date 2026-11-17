# codive — Local Code Knowledge Graph for AI Coding Agents

> **Turn any codebase into a local, queryable symbol and call graph. SQLite-backed symbol lookups, signature-only file skeletons, and blast-radius analysis for Claude Code, Cursor, Antigravity, VS Code, Windsurf and more. Local-first: no vector store, no cloud.**

[![CI](https://github.com/recscse/codive/actions/workflows/ci.yml/badge.svg)](https://github.com/recscse/codive/actions)
[![Release](https://img.shields.io/github/v/release/recscse/codive?style=flat-square)](https://github.com/recscse/codive/releases)
[![Downloads](https://img.shields.io/github/downloads/recscse/codive/total?style=flat-square&color=20c20e&label=Downloads)](https://github.com/recscse/codive/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/recscse/codive)](https://goreportcard.com/report/github.com/recscse/codive)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](LICENSE)

---

## 🏛️ Why codive? (Understand Your Code — Don't Grep It)

When AI coding assistants (Claude Code, Cursor, Google Antigravity, VS Code, Windsurf) work on real-world repositories, they face three bottlenecks:
1. **Token Exhaustion & Lost-in-the-Middle**: Dumping multi-thousand-line files into the context window uses up the context window on code the agent doesn't need.
2. **Slow, Blind Grep**: Raw regex searches take seconds on large repos and lack cross-file caller awareness, causing agents to break downstream API contracts.
3. **Fuzzy Vector Drift**: Vector embeddings guess approximate semantic similarity rather than deterministic syntactic truth, hallucinating caller connections.

`codive` solves this by indexing your repository's symbol declarations and call relations into an embedded SQLite database (WAL mode): a real parser for Go, and pattern-based extraction for the other languages (see *Supported Languages* and *Known Limits* below). It exposes **18 MCP tools** that return exact function bodies, token-compressed skeletons, upstream caller trees, and test pairings.

---

## 📦 Three Tangible Outputs Every Run

Running `codive init` or `codive serve` produces three deterministic local assets:

1. **`.codive/index.db` (Local SQLite WAL Engine)**:
   A lightweight local database storing symbol tables, caller/callee relations, and FTS5 full-text indexes. Nothing leaves your machine.
2. **`codive web` (Local Code Explorer)**:
   An embedded HTTP server at `http://127.0.0.1:7890` with symbol search, a package map, callers and callees, execution flow, blast radius, and a live source view. It works offline; use `--port` to change the port.
3. **18 MCP Tools**:
   Auto-registers seamlessly into Claude Code, Cursor, Antigravity, and VS Code with one command (`codive setup`).

---

## ⚖️ Architectural Matrix: Vector RAG vs. Grep vs. codive

| Capability | Vector RAG (Embeddings) | Raw File Grep | `codive` Symbol & Call Graph |
| :--- | :--- | :--- | :--- |
| **Parsing Precision** | Approximate / Semantic drift | Flat regex strings | **Deterministic symbol index (same input, same output); name-based matching, not type-resolved** |
| **Token Consumption** | Chunks raw text | Dumps matching files | **Signatures and line ranges only (via skeletons); see the benchmark report for measured sizes** |
| **Cross-File Call Graph** | Blind | Manual regex | **Caller & callee lookup by name; can include same-named symbols** |
| **Refactoring Safety** | Hallucinates safety | Blind guesses | **`blast_radius` + auto-linked test suites** |
| **Agent Memory** | Static context | No memory | **`save_decision` durable SQLite memory** |
| **Runtime & Privacy** | Heavy Python / Cloud API | Local | **Single compiled Go binary, 100% local** |

---

## 🚀 Key Features

- **⚡ Local Queries**: Local SQLite with Write-Ahead Logging (`WAL`), `synchronous = NORMAL`, and `busy_timeout = 10000ms` serves symbol lookups locally.
- **🦴 Token-Compressed Skeletons (`get_file_skeleton`)**: Replaces function and method bodies with `// L45–L92: label` line ranges and `{ ... }`, so an agent sees a file's whole API at a fraction of its size and then reads only the lines it needs.
- **📦 1-Shot Feature Bundling (`pack_feature_context`)**: Gathers the stored decisions, types and interfaces, call relationships, file skeletons, implementation functions and test suites that match a topic into one reply, ordered so the code to work on and its tests come last. The topic is split into words (`VerifyCredentials` becomes `verify`, `credentials`) and widened with a built-in synonym list, so `login` also looks for `auth`, `credential` and `verify`. The list is hand-written and covers common concepts only (login, auth, rate limits, database, cache, config, tokens, tests, routes, search, workers); a topic outside it matches on its own words.
- **🗺️ Proportional Architecture Maps (`get_repo_map`)**: Employs two-pass proportional tree budgeting so all modules (including late-alphabet folders like `services/`, `utils/`, `web/`) are guaranteed representation without truncation.
- **🧠 Session Working-Set Deduplication**: In multi-turn chat sessions with AI agents, re-queries for already-delivered files or symbols return compact reference anchors instead of repeating the content.
- **💣 Blast Radius Analysis (`blast_radius`)**: Traces direct callers, impacted files, and linked unit test suites before modifying code to prevent breaking changes.
- **🔒 Local & Private**: Indexes live in `.codive/index.db` and codive sends no telemetry; its only network use is `codive upgrade`. Credential-looking files (`.env`, `*.pem`, `id_rsa`, …) are never indexed, and the file tools refuse to read them.
- **🔁 One Shared Process for All Clients**: Every AI client talks to a single background daemon, so each index has exactly one writer. See *One Shared Process* below.
- **🔄 Auto-Configuring Multi-Client Setup (`codive setup`)**: Detects and registers the MCP server in Claude Code, Cursor, VS Code, Claude Desktop, Windsurf, Gemini CLI, Antigravity, Cline / Roo Code and Codex CLI.
- **🩺 `codive doctor`**: Finds the problems that make large repositories slow or wrong: a second index nested inside a repository, an index over 1 GB, an outdated symbol extractor, recent sync failures, `serve` processes running without the daemon, and a daemon of the wrong version.

---

## 📊 Benchmarks & Token Cost Savings

Output size of one command against its usual alternative, measured on this repository in a single run (tokens estimated as characters / 4). Full method and raw results: [the benchmark report](docs/BENCHMARK_REPORT.md).

| Task | Standard tool | codive | Change |
| :--- | :--- | :--- | :--- |
| Uncommitted changes (38 files) | `git diff`: 36,661 tokens | 1,906 tokens | 94.8% smaller |
| Outline of a 2,088-line file | full file: 16,956 tokens | 755 tokens | 95.5% smaller |
| Full-text search | `git grep`: 1,715 tokens | 974 tokens | 43.2% smaller |
| Find a function definition | `git grep -n`: 102 tokens | 101 tokens | about the same |
| Cross-file references | `git grep -n`: 370 tokens | 521 tokens | 40.8% **larger** |

These compare the size of one command's output. They do **not** show that an agent finishes tasks in fewer turns, tokens or minutes; that measurement ([`scripts/agentbench`](scripts/agentbench/README.md)) exists but has not been run yet, so no such claim is made.

---

## 🌐 Supported Languages & Symbol Extractors

| Language | How it is read | What is extracted |
| :--- | :--- | :--- |
| **Go** | Real parser (`go/parser`) | Functions, methods (with receiver), structs, interfaces, other types and aliases |
| **TypeScript / JavaScript** | Line patterns | Functions, arrow-function constants, classes, class methods, interfaces, type aliases, enums |
| **Python** | Line patterns | Functions and `async` functions (methods included, listed as functions), classes, with their decorators |
| **Java** | Line patterns | Classes, interfaces, records, enums, methods and constructors, Spring annotations |
| **C#** | Line patterns (shared with Java) | Classes, interfaces, records, enums, methods |
| **Rust** | Line patterns | Functions, structs, enums, traits, `impl` blocks |
| **`pom.xml`, `build.gradle`, `package.json`** | Structured parse | Project and dependency entries |

Declarations whose parameter list spans several lines are handled. Everything else (C/C++, Kotlin, Ruby, PHP, shell, SQL, YAML, …) is indexed for **full-text search only**: `search_code` finds it, `find_symbol` and the call tools do not. Not extracted today: imports, C# properties, structs and namespaces, Rust macros, Python docstrings as symbols.

---

## 📥 Installation

### Windows (PowerShell)
```powershell
irm https://recscse.github.io/codive/install.ps1 | iex; codive setup
```

### macOS / Linux (bash/curl)
```bash
curl -fsSL https://recscse.github.io/codive/install.sh | bash && codive setup
```

### Windows (Command Prompt / CMD via curl)
```cmd
curl -fsSL https://recscse.github.io/codive/install.cmd -o install.cmd && install.cmd && del install.cmd
```

### Via Go
```bash
go install github.com/recscse/codive@latest
```

### Upgrading
```bash
codive upgrade
```
Restart your AI clients afterwards so they use the new version. A running shared daemon of an older version is replaced automatically by the next client that connects.

---

## ⚡ Quickstart

### 1. Initialize Index
Navigate to any Git repository and run:
```bash
cd /path/to/project
codive init
```
Example output (numbers vary):
```text
Indexing Codebase [████████████████████████████] 100% (171/171 files in 0.11s | 1,533 files/s) - Symbols & AST extracted

● codive — Repository Index Initialization
────────────────────────────────────────────────────────────
  Indexed Files:     171 files (708.88 KB)
  AST Symbols:       720 symbols
  Language:          Java
  Database Path:     C:\work\project\.codive\index.db
  Latency:           1.120s
────────────────────────────────────────────────────────────
✔ Repository successfully indexed into local SQLite (WAL mode)!
```

### 2. Auto-Connect AI Assistants
```bash
codive setup
```
`codive setup` registers the MCP server with every AI client it detects on your machine. It doesn't configure clients you don't have installed.

| Client | Where codive is registered |
| :--- | :--- |
| **Claude Code** | `.mcp.json` in the project |
| **Cursor** | `.cursor/mcp.json` in the project |
| **VS Code (Copilot agent mode)** | `.vscode/mcp.json` in the project |
| **Claude Desktop** | `claude_desktop_config.json` |
| **Windsurf** | `~/.codeium/windsurf/mcp_config.json` |
| **Gemini CLI** | `~/.gemini/settings.json` |
| **Google Antigravity** | `~/.gemini/config/mcp_config.json` |
| **Cline / Roo Code** | their `cline_mcp_settings.json` |
| **Codex CLI** | `~/.codex/config.toml` |

It edits only codive's own entry, so your other MCP servers, their `env` settings, and every other setting are left exactly as they were. Before first changing an existing file, it saves the original next to it as `*.codive-backup`. If a config file contains comments, setup leaves it alone and prints the entry for you to add by hand. Continue is not edited automatically.

It also adds a short "how to use codive" section to your agent instruction files (`CLAUDE.md`, `AGENTS.md`, `.cursor/rules/codive.mdc`, …), inside `<!-- codive:start -->` / `<!-- codive:end -->` markers. Your own instructions in those files are not touched.

```bash
codive setup --dry-run   # show what would change, without writing anything
codive setup --undo      # remove everything setup added
```

### 3. Check that it works
Restart your AI client, then:
```bash
codive doctor            # index, extractor version, nested indexes, sync failures, client registration
codive daemon status     # the shared process and the repositories it has open
```
The first time a client opens a repository, codive builds its index in the background. A tool call made before it finishes says so and can be repeated; on a large repository this takes minutes.

---

## 🛠️ The 18 MCP Tools

When running as an MCP server (`codive serve`), your AI coding agent has access to:

| Tool | Parameters | Description |
| :--- | :--- | :--- |
| **`pack_feature_context`** | `topic` *(string)*, `workspace_path` *(opt)* | One-shot feature discovery: bundles symbol matches, skeletons, test suites, and stored decisions matching a topic. |
| **`get_file_skeleton`** | `path` *(string)*, `workspace_path` *(opt)* | Returns a structural outline with `// L45–L92` line ranges and `{ ... }` in place of function bodies. |
| **`blast_radius`** | `symbol` *(string)*, `workspace_path` *(opt)* | Traces all upstream callers, affected files, and test suites before refactoring. |
| **`find_symbol`** | `query` *(string)*, `workspace_path` *(opt)* | Returns exact declaration file, line number, signature, and symbol kind. |
| **`find_callers`** | `symbol` *(string)*, `limit` *(opt)*, `workspace_path` *(opt)* | Discovers all call sites and functions invoking the specified symbol. |
| **`find_callees`** | `symbol` *(string)*, `workspace_path` *(opt)* | Discovers internal functions and dependencies called inside a target function. |
| **`find_references`** | `symbol` *(string)*, `limit` *(opt)*, `workspace_path` *(opt)* | Finds references and usages across the entire codebase. |
| **`find_tests_for`** | `target` *(string)*, `workspace_path` *(opt)* | Resolves the corresponding unit test suite (`*_test.go`, `test_*.py`, `*.spec.ts`). |
| **`get_repo_map`** | `workspace_path`, `max_depth`, `directory_filter`, `include_symbols`, `token_budget` *(all opt)* | Emits a token-budgeted structural architecture map with declared symbols. |
| **`get_git_changes`** | `workspace_path` *(opt)* | Summarizes uncommitted git diffs mapped to the functions that enclose each change. |
| **`search_code`** | `query` *(string; wrap in double quotes for an exact phrase)*, `limit` *(opt)*, `max_lines_per_file` *(opt)*, `workspace_path` *(opt)* | Full-text search across all indexed files. Lists every matching line with its line number, grouped by file. |
| **`read_symbol`** | `symbol` *(string: `Name`, `Type.Method`, or `path:Name`)*, `path` *(opt)*, `limit` *(opt)* | Returns just one definition — doc comment, signature, and full body — instead of the whole file. |
| **`read_file_context`** | `path` *(string)*, `start_line`/`end_line` *(opt)*, `workspace_path` *(opt)* | Reads a file (up to 1,000 lines per call) plus its declared symbol outline, with automatic line-drift verification. |
| **`save_decision`** | `topic` *(string)*, `summary` *(string)*, `workspace_path` *(opt)* | Stores persistent architectural invariants and decisions in SQLite. |
| **`get_decisions`** | `topic` *(opt)*, `workspace_path` *(opt)* | Retrieves architectural memory and past decisions matching a topic. |
| **`find_files`** | `pattern` *(string)*, `limit` *(opt)*, `workspace_path` *(opt)* | Finds indexed files by name pattern. |
| **`get_execution_flow`** | `node_id` *(string)*, `depth` *(opt)*, `workspace_path` *(opt)* | Traverses the call flow from a node in the code graph. |
| **`get_codebase_graph`** | `limit` *(opt)*, `workspace_path` *(opt)* | Returns the code graph (nodes and edges) the explorer is drawn from. |

---

## 💻 CLI Command Reference

```bash
# Repository Indexing & Health
codive init [path]           # Initialize repository with parallel workers & live progress bar
codive update [path]         # Incrementally synchronize modified files
codive reindex [path]        # Clean wipe and rebuild index from scratch
codive watch [path]          # Live file watcher auto-syncing changes in real-time

# Code Navigation & Analysis
codive search "<query>"      # Instant full-text search
codive symbol <name>         # Find symbol declarations
codive refs <name>           # Find call sites & usages
codive blast <name>          # Analyze PR blast radius impact
codive pack <feature>        # Output token-optimized context pack for LLMs
codive map [path] [--web]    # Structural hierarchy map or interactive web visualization
codive diff [path]           # AST-aware summary of uncommitted git changes

# Setup & Configuration
codive setup [path]          # Auto-configure MCP for installed AI agents
codive install-hooks [path]  # Install Git post-commit & post-checkout sync hooks
codive init-rules [path]     # Auto-generate AI agent architecture rules files

# Diagnostics & Info
codive status [path]         # Display file counts, language breakdown, and index health
codive doctor [path]         # Check the index, the daemon, git, and client configurations
codive stats [path]          # Queries served and measured token counts
codive decisions [topic]     # View recorded architectural invariants & decisions
codive logs [path]           # Tail recent log entries
codive clean [path]          # Show disk used by indexes, or delete one project's index
codive config [set KEY VAL]  # Show or change this project's settings
codive bench [path]          # Measure intent search on the repo's own git history
codive ranker [history|reset] # What intent ranking has learned; learn from history; forget it

# Runtime
codive web [path] [--port N] # Launch the local code explorer
codive serve [path]          # MCP server for an AI client (forwards to the shared daemon)
codive daemon [start|stop|status]  # The shared background process behind serve
codive upgrade               # Self-update codive binary to latest release
codive version               # Show version and build info
codive about                 # Project background, author, and license
```

---

## 🔁 One Shared Process for All Your AI Clients

Every AI client starts its own MCP server. Instead of a dozen processes syncing and writing the same index, `codive serve` is a thin forwarder to **one** background daemon per user:

- The first `codive serve` starts the daemon (detached, listening on loopback only, guarded by a random token). Every other client reuses it, so each index has exactly one writer.
- The workspace is the nearest existing index above the folder the client started in, otherwise the root of the git repository, otherwise the folder itself. One repository gets one index, wherever the client was launched.
- A repository stays open while clients use it and for 5 minutes after; the daemon exits after 10 minutes with no clients.
- After an upgrade, the next client replaces the old daemon. If the daemon dies, the forwarder reconnects and replays the client's handshake; only requests in flight get an error.
- The first reply never waits for indexing: a tool call that arrives while a repository's first index is being built says so instead of hanging.

```bash
codive daemon status   # what is running and which repositories are open
codive daemon stop     # stop it (it starts again by itself with the next client)
```
Set `CODIVE_STANDALONE=1` to serve from each client's own process instead. The daemon logs to `codive/daemon.log` in your user cache folder (`%LOCALAPPDATA%` on Windows).

---

## 🔒 What Is Indexed, and Safety Defaults

- **SQLite settings**: WAL mode, `synchronous = NORMAL`, and a 10 s `busy_timeout`. Index writes are batched in one transaction per sync, and syncs run in the background so a slow one never blocks a tool call.
- **Respects your ignores**: `.gitignore` files are honored, `.codiveignore` adds your own patterns, and these are always skipped: `.git`, `node_modules`, `vendor`, `target`, `build`, `dist`, `bin`, `obj`, `.venv`, `.gradle`, `.mvn`, `.idea`, `.vscode`, `coverage`, `.next`, `.nuxt`, caches, binary files, and files over 5 MB.
- **Credentials are never indexed**: `.env` (but not `.env.example`), `*.pem`, `*.key`, `*.p12`, `*.jks`, `*.tfstate`, `id_rsa` and similar. The file-reading tools refuse them too.
- **`.codive/` ignores itself in git**, so the index never shows up as untracked files.

### Large repositories
The first index of a big repository takes minutes, and its size follows the repository's size: generated folders and large text or JSON files are the usual cause of a multi-gigabyte index. List them in `.codiveignore` and run `codive reindex`. `codive doctor` warns about an index over 1 GB and about a second index nested inside the repository, and `codive clean` shows what each project's index costs.

---

## ⚠️ Known Limits

- **Pattern-based extraction.** Apart from Go, symbols are found by line patterns, not a full parser. Unusual formatting can still be missed, so if `find_symbol` finds nothing for a symbol you know exists, confirm with `grep` before concluding it isn't there.
- **Lookups are by name.** `find_callers` and `find_references` match names, so unrelated symbols that share a name can appear together. Caller accuracy was checked against `gopls` for Go only ([the method](https://recscse.github.io/codive/blog/checking-codive-against-gopls.html)); other languages have not been measured.
- **No extractor, no symbols.** Languages not listed under *Supported Languages* are searchable as text only.
- **Agent results are unmeasured.** The benchmarks above compare output size; see `scripts/agentbench` for the planned end-to-end measurement.

---

## 🗑️ Uninstalling

```bash
codive daemon stop       # stop the shared background process
codive clean             # list every index codive has made and what each uses
codive clean <path>      # delete one project's index (your code is not touched)
```
Then remove the binary:
```bash
# macOS / Linux
rm "$(command -v codive)"
```
```powershell
# Windows (PowerShell): remove the binary and its user PATH entry
Remove-Item "$env:LOCALAPPDATA\codive" -Recurse -Force
```
Remove codive's entries from your AI clients with `codive setup --undo` first.

`.codive/index.db` is per-repository and never leaves your machine, so deleting a repo's `.codive/` folder (or running `codive reindex`) is enough to reclaim disk space without a full uninstall.

---

## 📚 Documentation, Support & Contributing

- **Full documentation**: [recscse.github.io/codive/docs.html](https://recscse.github.io/codive/docs.html) — installation, complete CLI reference, MCP tool schemas, editor integrations, and architecture internals.
- **Engineering blog**: [recscse.github.io/codive/blog.html](https://recscse.github.io/codive/blog.html) — deep-dives into the AST index, SQLite WAL concurrency, and the MCP tool internals.
- **Bug reports & feature requests**: [GitHub Issues](https://github.com/recscse/codive/issues).
- **Contributing**: see [CONTRIBUTING.md](CONTRIBUTING.md) for the dev setup, how to add a new language AST parser, and the PR process.
- **Security issues**: see [SECURITY.md](SECURITY.md) for how to report a vulnerability privately.
- **Release history**: see [CHANGELOG.md](CHANGELOG.md) for what shipped in each version.

---

## 📄 License

MIT © [Brijesh Yadav](https://github.com/recscse)
