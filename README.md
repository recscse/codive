# ⚡ devctx — Developer Context Engine for AI Coding Agents

> **High-performance, local-first code context engine & Model Context Protocol (MCP) server for autonomous AI coding agents.**

[![CI](https://github.com/recscse/devctx/actions/workflows/ci.yml/badge.svg)](https://github.com/recscse/devctx/actions)
[![Release](https://img.shields.io/github/v/release/recscse/devctx?style=flat-square)](https://github.com/recscse/devctx/releases)
[![Downloads](https://img.shields.io/github/downloads/recscse/devctx/total?style=flat-square&color=20c20e&label=Downloads)](https://github.com/recscse/devctx/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/recscse/devctx)](https://goreportcard.com/report/github.com/recscse/devctx)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](LICENSE)

---

## 🏛️ Why devctx?

When AI coding assistants (Claude Desktop, Cursor, Google Antigravity, VS Code, Windsurf) work on real-world repositories, they face two massive bottlenecks:
1. **Token Exhaustion**: Dumping multi-thousand-line files into the context window burns 8,000+ tokens per turn ($0.05–$0.25 per query) and leads to LLM "lost-in-the-middle" hallucinations.
2. **Slow, Blind Grep**: Raw file regex searches take seconds on large repos and lack cross-file caller awareness, causing agents to break downstream API contracts.

`devctx` solves this by indexing your repository's Abstract Syntax Tree (AST), symbol declarations, and call graphs into an embedded, high-concurrency SQLite database (WAL mode). It exposes **14 specialized MCP tools** that return exact function signatures, structural file skeletons, caller graphs, and test pairings in **< 2ms**.

---

## 🚀 Key Features

- **⚡ Sub-Millisecond Queries**: Local SQLite with Write-Ahead Logging (`WAL`), `synchronous = NORMAL`, and `busy_timeout = 10000ms` delivers symbol lookups in `< 1.8ms`.
- **🦴 Token-Compressed Skeletons (`get_file_skeleton`)**: Replaces function and method bodies with `{ /* L45-L92 */ }` line references, reducing token consumption by **95–98%** while preserving structural awareness.
- **📦 1-Shot Feature Bundling (`pack_feature_context`)**: Aggregates route handlers, data models, skeletons, and test suites matching a feature in a single query turn.
- **💣 Blast Radius Analysis (`blast_radius`)**: Traces direct callers, impacted files, and linked unit test suites before modifying code to prevent breaking changes.
- **🔒 100% Local & Private**: All indexes live in `.devctx/index.db`. Zero cloud telemetry, zero remote tracking, zero external data leakage.
- **🔄 Auto-Configuring Multi-Client Setup (`devctx setup`)**: Automatically detects and registers the MCP server in Claude Desktop, Cursor IDE, Google Antigravity, and VS Code.

---

## 📊 Benchmarks & Token Cost Savings

Tested on a production monorepo containing **100,000+ lines across 1,500 files**:

| Operation | Traditional LLM Approach | `devctx` MCP Engine | Performance Advantage |
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
irm https://recscse.github.io/devctx/install.ps1 | iex; devctx setup
```

### macOS / Linux (bash/curl)
```bash
curl -fsSL https://recscse.github.io/devctx/install.sh | bash && devctx setup
```

### Windows (Command Prompt / CMD via curl)
```cmd
curl -fsSL https://recscse.github.io/devctx/install.cmd -o install.cmd && install.cmd && del install.cmd
```

### Via Go
```bash
go install github.com/recscse/devctx@latest
```

---

## ⚡ Quickstart

### 1. Initialize Index
Navigate to any Git repository and run:
```bash
cd /path/to/project
devctx init
```
```text
Indexing Codebase [████████████████████████████] 100% (171/171 files in 0.11s | 1,533 files/s) - Symbols & AST extracted

● devctx — Repository Index Initialization
────────────────────────────────────────────────────────────
  Indexed Files:     171 files (708.88 KB)
  AST Symbols:       720 symbols
  Language:          Java
  Database Path:     C:\work\project\.devctx\index.db
  Latency:           1.120s
────────────────────────────────────────────────────────────
✔ Repository successfully indexed into local SQLite (WAL mode)!
```

### 2. Auto-Connect AI Assistants
```bash
devctx setup
```
`devctx setup` automatically registers the MCP server configuration into:
- **Google Antigravity**: `~/.gemini/config/mcp_config.json`
- **Cursor IDE**: `~/.cursor/mcp.json`
- **Claude Desktop**: `~/Library/Application Support/Claude/claude_desktop_config.json` or `%APPDATA%\Claude\claude_desktop_config.json`
- **VS Code**: `.vscode/mcp.json` / `~/.continue/config.json`

---

## 🛠️ The 14 Production MCP Tools

When running as an MCP server (`devctx serve`), your AI coding agent has access to:

| Tool | Parameters | Description |
| :--- | :--- | :--- |
| **`pack_feature_context`** | `query` *(string)*, `path` *(opt)* | One-shot feature discovery: bundles models, routes, skeletons, and test suites matching a topic. |
| **`get_file_skeleton`** | `file_path` *(string)* | Returns structural outline with `{ /* L45-L92 */ }` line ranges, cutting 95% of tokens. |
| **`blast_radius`** | `symbol` *(string)*, `path` *(opt)* | Traces all upstream callers, affected files, and test suites before refactoring. |
| **`find_symbol`** | `name` *(string)*, `path` *(opt)* | Returns exact declaration file, line number, signature, and symbol kind. |
| **`find_callers`** | `symbol` *(string)*, `path` *(opt)* | Discovers all call sites and functions invoking the specified symbol. |
| **`find_callees`** | `symbol` *(string)*, `path` *(opt)* | Discovers internal functions and dependencies called inside a target function. |
| **`find_references`** | `name` *(string)*, `path` *(opt)* | Finds references and usages across the entire codebase. |
| **`find_tests_for`** | `file_or_symbol` *(string)* | Resolves the corresponding unit test suite (`*_test.go`, `test_*.py`, `*.spec.ts`). |
| **`get_repo_map`** | `token_budget` *(opt)* | Emits a token-budgeted structural architecture map with declared symbols. |
| **`get_git_changes`** | `path` *(opt)* | Summarizes uncommitted git diffs mapped to enclosing AST functions. |
| **`search_code`** | `query` *(string)*, `limit` *(opt)* | Sub-millisecond FTS5 full-text search across all indexed files. |
| **`read_file_context`** | `file_path` *(string)*, `start_line`, `end_line` | Reads targeted line ranges with automatic line-drift verification. |
| **`save_decision`** | `topic` *(string)*, `decision` *(string)* | Stores persistent architectural invariants and decisions in SQLite. |
| **`get_decisions`** | `topic` *(string)* | Retrieves architectural memory and past decisions matching a topic. |

---

## 💻 CLI Command Reference

```bash
# Repository Indexing & Health
devctx init [path]           # Initialize repository with parallel workers & live progress bar
devctx update [path]         # Incrementally synchronize modified files in <100ms
devctx reindex [path]        # Clean wipe and rebuild index from scratch
devctx status [path]         # Display file counts, language breakdown, and index health
devctx doctor [path]         # Verify SQLite schema, git status, and client configurations

# Code Navigation & Analysis
devctx search "<query>"      # Instant full-text search
devctx symbol <name>         # Find symbol declarations
devctx refs <name>           # Find call sites & usages
devctx blast <name>          # Analyze PR blast radius impact
devctx pack <feature>        # Output token-optimized context pack for LLMs
devctx map [path] [--web]    # Structural hierarchy map or interactive web visualization
devctx decisions [topic]     # View recorded architectural invariants & decisions

# Concurrency & Automation
devctx watch [path]          # Live file watcher auto-syncing changes in real-time
devctx install-hooks [path]  # Install Git post-commit & post-checkout sync hooks
devctx upgrade               # Self-update devctx binary to latest release
```

---

## 🔒 Concurrency, Multi-Process Safety & Lock Resilience

`devctx` is designed from the ground up for high-concurrency multi-agent environments:
- **Embedded DSN Pragmas**: SQLite connections are created with `_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-64000)`.
- **Zero Database Lock Deadlocks**: Built-in exponential backoff retry loops prevent `database is locked (261)` errors during IDE restarts.
- **Smart Monorepo Ignores**: Automatically prunes `.git`, `node_modules`, `target`, `build`, `dist`, `.venv`, `.gradle`, `.mvn`, and `jacoco-aggregator`.

---

## 📄 License

MIT © [Brijesh Yadav](https://github.com/recscse)
