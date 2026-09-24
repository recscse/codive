# Changelog

All notable changes to `codive` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Fixed
- `find_references`, `find_callers`, and `blast_radius` could return **nothing at all** for common identifiers on large repositories (e.g. `Fprintf` with ~2,900 references in the Go standard library): the reference scan built an FTS snippet for every candidate file, ran one extra query per candidate, and re-fetched each file's content, so it ran past the tool-call deadline. References are now streamed straight from the full-text index and stop at the requested limit (`find_references` on a 10k-file repo: 1.3s → under 10ms). Measured against `grep -w` on five common symbols: 0 missed, 0 extra.
- Capped results no longer read as complete. `find_references`/`find_callers` say "MORE EXIST" when the limit was hit (previously an agent was told e.g. "Found: 30" for a symbol with thousands of references), `blast_radius` reports "N+" with an explicit lower-bound note, and `find_symbol` returns at most 30 matches (configurable via `limit`) with a "showing N of M" header — a broad query like `New` previously returned ~100,000 tokens in one response.
- `find_references` matched substrings (`Scan` matched `ScanIncremental`, `rescan`), and `find_callers` could miss real callers when a symbol was mostly mentioned in comments or imports. Both now match whole identifiers and filter during the scan.
- Re-running `codive init` kept symbols from deleted files and duplicated any symbol whose line number had moved. `init` now replaces the index atomically.
- `find_tests_for` found test files but never listed the tests inside them.
- Reading a file through the MCP server (e.g. `read_file_context` on `node_modules/.env`) could pull ignored files into the full-text index, and every `find_symbol` match triggered a full re-parse and four table rewrites even for unchanged files. Files are now only re-parsed when already indexed and actually changed.
- `get_git_changes` mishandled paths with spaces or non-ASCII characters (git C-quotes them), collapsed new untracked directories into one unreadable entry, and listed affected symbols in random order.
- Commands that take a symbol name (e.g. `codive blast GenerateToken`) created a stray `./GenerateToken/.codive/` log folder in the current directory.
- A crash between a schema migration and its version bump left the database failing to open with "duplicate column" on every start. Migrations are now transactional.
- A crash mid-sync could leave a file marked as indexed at its new content hash while still holding the previous version's symbols. All index writes for a batch now happen in one transaction.

### Changed
- **Freshness is now guaranteed at query time**: before answering, the MCP server syncs the index if its last sync is more than 2s old, and the background poll adapts to repository size. On a 10k-file repository this cut idle CPU from ~33% of a core to ~3% while new files still become visible within ~1–2s.
- No-op rescans no longer reopen files already known to be binary (0.9s → 0.25s per rescan on a 10k-file repository).
- Every MCP tool call now runs under a 60s deadline, and `get_git_changes` runs a fixed number of git processes (one streamed diff for the whole repo instead of one per changed file), listing at most 500 files and analyzing at most 300 — with any skipped files disclosed in the output.
- `read_file_context` returns at most 1,000 lines per call and accepts `start_line`/`end_line`.
- MCP auto-indexing of an agent-supplied `workspace_path` is limited to the configured workspace or a git repository root.
- The MCP server negotiates the protocol version (`2025-06-18`, `2025-03-26`, `2024-11-05`) and accepts JSON-RPC batches.
- `find_references`/`find_callers` output is grouped by file, using 10–25% fewer tokens than the equivalent `grep -rn` output.
- Indexing (`init`, `update`, `watch`, `serve` auto-sync, MCP auto-index) now shares one implementation instead of four drifted copies.

---

## [v1.1.1] - 2026-09-07

### Fixed
- `codive serve` corrupted the MCP stdio JSON-RPC transport with human-readable progress-bar and summary text on the very first connection to any workspace that hadn't been `codive init`'d yet — exactly the state `codive setup`'s automated onboarding leaves every new user in. Auto-indexing on first connect now runs silently.
- `codive stats` fabricated baseline numbers (12 queries, 32,500 tokens saved, 38.4s latency saved) for a fresh install with no recorded usage, and reported a hardcoded 4.8x/5.2x "speed multiplier" regardless of actual activity. Now computed entirely from real recorded telemetry, and correctly reports all-zeros before any tool has been called.
- `get_git_changes` had a status-parsing bug where trimming the *entire* `git status --porcelain` output (instead of just its trailing newline) ate the leading status-column space off the first changed file only, silently truncating its path (e.g. `tracked.go` reported as `racked.go`).
- `get_git_changes` always reported 0 changed lines and no affected symbols for brand-new (untracked) files, since `git diff` has nothing to compare them against. Its enclosing-symbol lookup also used a fuzzy "search by file path as if it were a symbol name" query that rarely matched anything, even for ordinarily modified/tracked files. Replaced with an exact per-file symbol lookup and a real new-file code path.
- Files whose mtime changed on disk but whose content hash didn't (e.g. a touch, or a checkout resetting timestamps) were never persisted with their refreshed metadata, so they were silently rehashed from scratch on every single scan, forever.

### Changed
- Deduplicated blast-radius and call-relationship helper logic that had drifted into byte-identical copies across the CLI and MCP server, so the two surfaces can no longer silently disagree.

---

## [v1.1.0] - 2026-09-03

### Added
- **Java & C# AST Support**: New symbol extractors for Java (including Spring annotations) and C#, plus `pom.xml` parsing for Maven projects.
- **Dynamic Workspace Routing & Auto-Indexing**: MCP tools now resolve the nearest `.codive/index.db` by walking up from the target path and auto-index on the fly when no index exists yet.
- **LLM-Optimized MCP Output**: Semantic role classification (e.g. "Test Case", "Type Definition") on `find_symbol`/`find_references` results, auto-recall of relevant `save_decision` entries injected into tool responses, and a per-response token-budget footer showing tokens used vs. saved.
- **Production-grade CLI UI redesign** with refreshed docs across README and the docs site.

### Fixed
- Progress bar and connection/tool-update issues in the MCP server.
- Slow initial indexing time on large repositories.
- MCP `initialize` response now reports the actual build-time binary version instead of a hardcoded, drifted string.
- `pack_feature_context` (both the MCP tool and `codive pack`) never returned a real code skeleton for any query — it passed the literal string `"auto"` as the language, which matched no case in the symbol extractor and silently produced an empty skeleton every time. Its file-relevance ranking was also alphabetical rather than relevance-based, letting unrelated docs crowd out the actually relevant source file.
- `find_callers` was a literal alias for `find_references` (identical results, including the symbol's own declaration as a "caller"). It's now a genuinely narrower query that excludes the declaration and requires an actual call expression.
- `pack_feature_context` had no caller/callee relationship data at all; it now includes a "Call Relationships" section for the top matched symbols.
- `find_callees` used a hardcoded 60-line scan window, silently truncating analysis for any function longer than that (common in this codebase itself); it also reported struct/type names from a function's own signature line as false-positive "callees." Both fixed.
- The TypeScript/JavaScript symbol extractor had no detection at all for ES6 class methods (only top-level `function` declarations and arrow-function exports were recognized), and its arrow-function pattern didn't account for an explicit return-type annotation — so class methods and many arrow functions were invisible to every MCP tool. Fixed and verified against a live multi-language test repo.
- `.gemini/rules/codive.md` referenced a nonexistent `puck` MCP tool instead of `codive` (a copy-paste leftover).

### Changed
- Project renamed from `devctx`/`ctxd` to `codive` throughout: Go module path, all agent-rules files, CI config, install scripts, docs, and rebrand assets.
- README, docs site, and blog corrected for accuracy (MCP tool parameter names, Go version requirements, fabricated C/C++ AST-support claim removed) and the blog rebuilt as real standalone pages instead of a JS-modal index with no crawlable URLs.

---

## [v1.0.0] - 2026-08-25 — *Inaugural Official Release*

Welcome to the initial release of **`codive`**, the high-performance local-first AST context engine and Model Context Protocol (MCP) server for AI coding assistants.

### 🌟 Core Highlights
- **14-Tool Model Context Protocol Suite**: Complete JSON-RPC 2.0 stdio server supporting Claude Code, Cursor, Windsurf, Google Antigravity, and VS Code.
- **One-Shot Feature Context Packer (`pack_feature_context` / `codive pack`)**: Bundles related routes, data models, schemas, skeletons, and test suites in 1 single turn (250 tokens vs. 8,000 tokens).
- **Code Skeletonizer (`get_file_skeleton`)**: Strips function bodies into `{ /* L45-L92 */ }` comments, turning 2,000-line files into concise 50-token skeletons.
- **PR Blast Radius Analyzer (`blast_radius` / `codive blast`)**: Answers *"If I change this symbol, what will break?"* by discovering direct callers, affected modules, and exact test suites to execute.
- **Real-Time Token & Money Savings Tracker (`codive stats`)**: Quantifies exact queries served, latency reduced, tokens saved vs. brute-force grep, and cloud dollar savings with embeddable README shields.
- **Interactive Web Architecture Map (`codive map --web` / `codive web`)**: Spins up a local browser UI on `localhost:7890` displaying an interactive radial force network graph connecting Files, Classes, Functions, and Callers.
- **Persistent Agent Memory (`save_decision` & `get_decisions`)**: Durable SQLite store allowing AI agents to record and retrieve architectural invariants.
- **Sub-Millisecond Line-Number Drift Protection**: Automatically detects edited files on-the-fly and micro-reparses in $<2\text{ms}$.
- **Multi-Language AST Parsers**: Deterministic lexical extractors for **Go**, **TypeScript / JavaScript**, **Python**, and **Rust**.
- **SQLite WAL Mode Engine**: Zero-lock concurrent database with symbol-boosted FTS5 full-text search.
- **Universal 1-Command Auto-Configurator (`codive setup`)**: Detects installed AI editors and configures MCP servers with `autoApprove: true`.
- **Git Hook Integration (`codive install-hooks`)**: Automatically synchronizes repository index on commits and branch switches.
