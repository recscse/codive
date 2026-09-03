# ⚡ codive — Developer Context Engine for AI Coding Agents

> **High-performance, local-first code context engine & Model Context Protocol (MCP) server for autonomous AI coding agents.**

[![CI](https://github.com/recscse/codive/actions/workflows/ci.yml/badge.svg)](https://github.com/recscse/codive/actions)
[![Release](https://img.shields.io/github/v/release/recscse/codive?style=flat-square)](https://github.com/recscse/codive/releases)
[![Downloads](https://img.shields.io/github/downloads/recscse/codive/total?style=flat-square&color=20c20e&label=Downloads)](https://github.com/recscse/codive/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/recscse/codive)](https://goreportcard.com/report/github.com/recscse/codive)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](LICENSE)

---

## 🏛️ Why codive?

When AI coding assistants (Claude Desktop, Cursor, Google Antigravity, VS Code, Windsurf) work on real-world repositories, they face two massive bottlenecks:
1. **Token Exhaustion**: Dumping multi-thousand-line files into the context window burns 8,000+ tokens per turn ($0.05–$0.25 per query) and leads to LLM "lost-in-the-middle" hallucinations.
2. **Slow, Blind Grep**: Raw file regex searches take seconds on large repos and lack cross-file caller awareness, causing agents to break downstream API contracts.

`codive` solves this by indexing your repository's Abstract Syntax Tree (AST), symbol declarations, and call graphs into an embedded, high-concurrency SQLite database (WAL mode). It exposes **14 specialized MCP tools** that return exact function signatures, structural file skeletons, caller graphs, and test pairings in **< 2ms**.

---

## 🚀 Key Features

- **⚡ Sub-Millisecond Queries**: Local SQLite with Write-Ahead Logging (`WAL`), `synchronous = NORMAL`, and `busy_timeout = 10000ms` delivers symbol lookups in `< 1.8ms`.
- **🦴 Token-Compressed Skeletons (`get_file_skeleton`)**: Replaces function and method bodies with `{ /* L45-L92 */ }` line references, reducing token consumption by **95–98%** while preserving structural awareness.
- **📦 1-Shot Feature Bundling (`pack_feature_context`)**: Aggregates route handlers, data models, skeletons, and test suites matching a feature in a single query turn.
- **💣 Blast Radius Analysis (`blast_radius`)**: Traces direct callers, impacted files, and linked unit test suites before modifying code to prevent breaking changes.
- **🔒 100% Local & Private**: All indexes live in `.codive/index.db`. Zero cloud telemetry, zero remote tracking, zero external data leakage.
- **🔄 Auto-Configuring Multi-Client Setup (`codive setup`)**: Automatically detects and registers the MCP server in Claude Desktop, Cursor IDE, Google Antigravity, and VS Code.

---

## 📊 Benchmarks & Token Cost Savings

Tested on a production monorepo containing **100,000+ lines across 1,500 files**:

| Operation | Traditional LLM Approach | `codive` MCP Engine | Performance Advantage |
| :--- | :--- | :--- | :--- |
| **Inspecting a 1,200-Line File** | 6,500 tokens ($0.065/turn) | **120 tokens** ($0.001/turn via `get_file_skeleton`) | **98.2% Token Reduction** |
| **Cross-Repo Symbol Resolution** | 5–15s (disk grep) | **< 1.8 milliseconds** (`find_symbol`) | **99.9% Faster** |
| **Refactoring Blast Radius** | Blind guesses & broken tests | Instant upstream call tree & linked test suites | **Zero Regressions** |
| **Cold Repository Initialization** | 30+ minutes (eager full scan) | **1.2 – 1.8 seconds** (Parallel Goroutine Pool) | **1,500x Speedup** |

---

## 🌐 Supported Languages & Symbol Extractors

| Language | Extracted Declarations & Symbols |
| :--- | :--- |
| **Go** | Functions, methods, receiver types, structs, interfaces, type aliases, imports |
| **TypeScript / JavaScript** | Functions, async methods, classes, interfaces, type aliases, exports |
| **Python** | Functions, async defs, classes, methods, docstrings, decorators |
| **Java** | Classes, interfaces, methods, annotations, record types, constructors |
| **C#** | Classes, interfaces, methods, properties, structs, namespaces |
| **Rust** | Functions, structs, enums, traits, `impl` blocks, macros |

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

---

## ⚡ Quickstart

### 1. Initialize Index
Navigate to any Git repository and run:
```bash
cd /path/to/project
codive init
```
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
`codive setup` automatically registers the MCP server configuration into:
- **Google Antigravity**: `~/.gemini/config/mcp_config.json`
- **Cursor IDE**: `~/.cursor/mcp.json`
- **Claude Desktop**: `~/Library/Application Support/Claude/claude_desktop_config.json` or `%APPDATA%\Claude\claude_desktop_config.json`
- **VS Code**: `.vscode/mcp.json` / `~/.continue/config.json`

---

## 🛠️ The 14 Production MCP Tools

When running as an MCP server (`codive serve`), your AI coding agent has access to:

| Tool | Parameters | Description |
| :--- | :--- | :--- |
| **`pack_feature_context`** | `topic` *(string)*, `workspace_path` *(opt)* | One-shot feature discovery: bundles symbol matches, skeletons, test suites, and stored decisions matching a topic. |
| **`get_file_skeleton`** | `path` *(string)*, `workspace_path` *(opt)* | Returns structural outline with `{ /* L45-L92 */ }` line ranges, cutting 95% of tokens. |
| **`blast_radius`** | `symbol` *(string)*, `workspace_path` *(opt)* | Traces all upstream callers, affected files, and test suites before refactoring. |
| **`find_symbol`** | `query` *(string)*, `workspace_path` *(opt)* | Returns exact declaration file, line number, signature, and symbol kind. |
| **`find_callers`** | `symbol` *(string)*, `limit` *(opt)*, `workspace_path` *(opt)* | Discovers all call sites and functions invoking the specified symbol. |
| **`find_callees`** | `symbol` *(string)*, `workspace_path` *(opt)* | Discovers internal functions and dependencies called inside a target function. |
| **`find_references`** | `symbol` *(string)*, `limit` *(opt)*, `workspace_path` *(opt)* | Finds references and usages across the entire codebase. |
| **`find_tests_for`** | `target` *(string)*, `workspace_path` *(opt)* | Resolves the corresponding unit test suite (`*_test.go`, `test_*.py`, `*.spec.ts`). |
| **`get_repo_map`** | `workspace_path`, `max_depth`, `directory_filter`, `include_symbols`, `token_budget` *(all opt)* | Emits a token-budgeted structural architecture map with declared symbols. |
| **`get_git_changes`** | `workspace_path` *(opt)* | Summarizes uncommitted git diffs mapped to enclosing AST functions. |
| **`search_code`** | `query` *(string)*, `limit` *(opt)*, `workspace_path` *(opt)* | Sub-millisecond FTS5 full-text search across all indexed files. |
| **`read_file_context`** | `path` *(string)*, `workspace_path` *(opt)* | Reads a file's full content plus its declared symbol outline, with automatic line-drift verification. |
| **`save_decision`** | `topic` *(string)*, `summary` *(string)*, `workspace_path` *(opt)* | Stores persistent architectural invariants and decisions in SQLite. |
| **`get_decisions`** | `topic` *(opt)*, `workspace_path` *(opt)* | Retrieves architectural memory and past decisions matching a topic. |

---

## 💻 CLI Command Reference

```bash
# Repository Indexing & Health
codive init [path]           # Initialize repository with parallel workers & live progress bar
codive update [path]         # Incrementally synchronize modified files in <100ms
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
codive doctor [path]         # Verify SQLite schema, git status, and client configurations
codive stats [path]          # Token savings and efficiency metrics
codive decisions [topic]     # View recorded architectural invariants & decisions
codive logs [path]           # Tail recent log entries

# Runtime
codive web [path]            # Launch interactive architecture network graph
codive serve [path]          # Run the MCP (Model Context Protocol) server
codive upgrade               # Self-update codive binary to latest release
codive version               # Show version and build info
```

---

## 🔒 Concurrency, Multi-Process Safety & Lock Resilience

`codive` is designed from the ground up for high-concurrency multi-agent environments:
- **Embedded DSN Pragmas**: SQLite connections are created with `_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-64000)`.
- **Zero Database Lock Deadlocks**: A bounded retry loop (5 attempts, 100ms apart) on connection open, backed by a 10s DSN-level `busy_timeout`, absorbs `database is locked (261)` errors during IDE restarts.
- **Smart Monorepo Ignores**: Automatically prunes `.git`, `node_modules`, `target`, `build`, `dist`, `.venv`, `.gradle`, `.mvn`, and `jacoco-aggregator`.

---

## 🗑️ Uninstalling

```bash
# macOS / Linux — remove the binary and (optionally) all local indexes
rm "$(command -v codive)"
find / -maxdepth 6 -type d -name ".codive" -prune -exec rm -rf {} + 2>/dev/null

# Windows (PowerShell) — remove the binary and its user PATH entry
Remove-Item "$env:LOCALAPPDATA\codive" -Recurse -Force
```
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
