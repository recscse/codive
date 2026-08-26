# devctx

Fast, local-first code context engine and Model Context Protocol (MCP) server for AI coding agents.

[![CI](https://github.com/recscse/devctx/actions/workflows/ci.yml/badge.svg)](https://github.com/recscse/devctx/actions)
[![Release](https://img.shields.io/github/v/release/recscse/devctx?style=flat-square)](https://github.com/recscse/devctx/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/recscse/devctx)](https://goreportcard.com/report/github.com/recscse/devctx)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg?style=flat-square)](LICENSE)

`devctx` indexes your repository's Abstract Syntax Tree (AST) into a local SQLite database and exposes 14 specialized query tools to AI coding agents (Claude Desktop, Cursor, Google Antigravity, Windsurf, VS Code) over standard MCP (JSON-RPC) or CLI.

Instead of running brute-force `grep` or dumping entire multi-thousand-line source files into the LLM context window, `devctx` returns exact function signatures, structural file skeletons, caller graphs, and test pairings in sub-millisecond queries.

---

## Features

- **Sub-Millisecond Queries**: Embedded SQLite with Write-Ahead Logging (WAL) mode delivers symbol searches and call graphs in `< 2ms`.
- **Structural File Skeletons**: Emits file outlines where function bodies are replaced with `{ /* L45-L92 */ }` line references, reducing token consumption while preserving structural awareness.
- **1-Shot Feature Bundling**: `pack_feature_context` aggregates route handlers, data models, skeletons, and test suites matching a feature in a single query turn.
- **Blast Radius Analysis**: Traces upstream callers and linked unit test suites before applying code edits to prevent regressions.
- **100% Local & Private**: Indexes your code locally in `.devctx/index.db`. Zero cloud telemetry or external network calls.
- **Zero-Config Editor Setup**: `devctx setup` automatically configures MCP integration for Claude Desktop, Cursor, Google Antigravity, and VS Code.

---

## Supported Languages

| Language | Extracted Symbols |
| :--- | :--- |
| **Go** | Functions, methods, structs, interfaces, type aliases, imports |
| **TypeScript / JavaScript** | Functions, classes, methods, interfaces, types, exports |
| **Python** | Functions, async functions, classes, methods, docstrings |
| **Rust** | Functions, structs, enums, traits, `impl` blocks |

---

## Installation

### macOS / Linux
```bash
curl -fsSL https://recscse.github.io/devctx/install.sh | bash && devctx setup
```

### Windows (PowerShell)
```powershell
irm https://recscse.github.io/devctx/install.ps1 | iex; devctx setup
```

### Via Go
```bash
go install github.com/recscse/devctx@latest
```

---

## Quick Start

### 1. Initialize Index

Run `devctx init` in the root of any Git repository:

```bash
cd /path/to/project
devctx init
```

```text
⚡ devctx - Indexing repository...
  Scanning files: 100% [==============================] (142 files)
  Parsing AST symbols...
✓ Indexed 142 files (580 symbols) into .devctx/index.db in 42ms
```

### 2. Connect to Your AI Assistant

Run `devctx setup` to automatically register the MCP server with your installed AI assistants:

```bash
devctx setup
```

```text
🔧 devctx Setup - AI Client MCP Configuration
  ✓ Configured Claude Desktop: ~/.config/Claude/claude_desktop_config.json
  ✓ Configured Cursor IDE: ~/.cursor/mcp.json
  ✓ Configured Google Antigravity: ~/.gemini/config/mcp_config.json
✨ devctx MCP server successfully registered across all clients!
```

---

## MCP Server Configuration

If you prefer manual configuration, add the following to your assistant's MCP config file (`mcp.json` or `claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "devctx": {
      "command": "devctx",
      "args": ["serve"]
    }
  }
}
```

---

## MCP Tools Reference

When running as an MCP server (`devctx serve`), the following 14 tools are available:

| Tool | Arguments | Description |
| :--- | :--- | :--- |
| `get_repo_map` | `max_tokens` (int, optional) | Returns a high-level repository symbol map within a token budget. |
| `get_file_skeleton` | `path` (string) | Returns an AST skeleton of a file with function signatures and line numbers. |
| `find_symbol` | `query` (string), `kind` (string, optional) | Searches for symbol declarations by name and kind (function, class, struct). |
| `find_callers` | `symbol` (string) | Finds all functions and files that call the specified symbol. |
| `find_callees` | `symbol` (string) | Lists functions and types called within a specific function body. |
| `find_tests_for` | `target` (string) | Finds unit tests corresponding to a target function or file. |
| `pack_feature_context` | `feature` (string) | Bundles all models, handlers, and skeletons for a feature into a single response. |
| `blast_radius` | `symbol` (string) | Assesses upstream impact and affected test suites for a symbol before refactoring. |
| `save_decision` | `topic` (string), `summary` (string) | Saves an architectural decision or constraint to local SQLite storage. |
| `get_decisions` | `topic` (string, optional) | Retrieves saved architectural decisions matching a topic. |
| `get_git_changes` | None | Returns uncommitted git diffs mapped to affected AST symbols. |
| `search_code` | `query` (string), `limit` (int, optional) | Full-text search (SQLite FTS5) with extracted context snippets. |
| `read_file_context` | `path` (string) | Reads file content with prepended symbol declaration metadata. |
| `find_references` | `query` (string), `limit` (int, optional) | Finds cross-file references and usages of an identifier. |

---

## CLI Usage

```bash
# Repository Indexing
devctx init [path]          # Scan and index a repository from scratch
devctx update [path]        # Incrementally scan modified files
devctx reindex [path]       # Rebuild index from scratch
devctx doctor [path]        # Run system health checks and verify MCP configurations
devctx stats [path]         # Show file and symbol statistics

# Code Inspection
devctx search <query>       # Search AST symbols from the terminal
devctx blast <symbol>       # Analyze refactoring blast radius
devctx pack <feature>       # Generate 1-turn bundled feature context
devctx map --web            # Launch interactive browser architecture map (http://localhost:7890)

# Server & Integration
devctx serve                # Start MCP JSON-RPC server over stdio
devctx setup                # Auto-wire MCP configurations for all AI clients
devctx init-rules           # Generate AGENTS.md / CLAUDE.md / .cursorrules
devctx install-hooks        # Install Git post-commit hooks for automatic re-indexing
```

---

## Architecture & Design

### SQLite Index Schema
`devctx` creates a local SQLite database at `.devctx/index.db` configured with WAL mode and normalized tables:
- `files`: File paths, content hashes (SHA-256), languages, sizes, and timestamps.
- `symbols`: Symbol names, kinds (`func`, `struct`, `class`, `interface`), signatures, and line numbers.
- `file_fts`: FTS5 virtual table for full-text search with BM25 ranking.
- `decisions`: Persistent key-value store for architectural notes recorded by agents.

### Automatic Updates vs. Manual Reindexing

| Operation | Command / Mechanism | How It Works |
| :--- | :--- | :--- |
| **On-The-Fly Drift Protection** | *Automatic on every tool call* | Checks file `ModTime` on disk. If dirty, micro-reparses the single file in `< 1ms` before answering MCP queries. |
| **Git Commit & Pull Sync** | `devctx install-hooks` | Installs Git `post-commit` and `post-checkout` hooks to auto-update SQLite on branch switches. |
| **Live File Watcher** | `devctx watch` | Runs a background filesystem watcher (fsnotify) to index files immediately on save. |
| **Manual Incremental Sync** | `devctx update` | Scans only files changed since the last indexed timestamp. |
| **Full Wipe & Rebuild** | `devctx reindex` | Completely wipes `.devctx/index.db` and rebuilds all symbols and FTS tables from scratch. |

### Privacy & Download Analytics
- **100% Local-First**: No code, embeddings, or queries ever leave your machine. `devctx` contains zero telemetry tracking code.
- **Tracking Public Adoption**: Project download statistics are tracked transparently via official GitHub Release binary downloads and GitHub Traffic Insights (`Insights > Traffic`).

### Security
- **Sandboxed File Access**: File operations validate paths against the repository root to prevent directory traversal (`../`).
- **Parameterized SQL**: All database operations use parameterized queries to prevent SQL injection.
- **Local-Only**: No external network requests are made during indexing or query execution.

---

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and testing instructions.

```bash
# Run tests
go test -v ./...

# Run linter
go vet ./...
```

---

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
