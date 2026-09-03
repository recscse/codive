# Changelog

All notable changes to `codive` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- **Java & C# AST Support**: New symbol extractors for Java (including Spring annotations) and C#, plus `pom.xml` parsing for Maven projects.
- **Dynamic Workspace Routing & Auto-Indexing**: MCP tools now resolve the nearest `.codive/index.db` by walking up from the target path and auto-index on the fly when no index exists yet.
- **LLM-Optimized MCP Output**: Semantic role classification (e.g. "Test Case", "Type Definition") on `find_symbol`/`find_references` results, auto-recall of relevant `save_decision` entries injected into tool responses, and a per-response token-budget footer showing tokens used vs. saved.
- **Production-grade CLI UI redesign** with refreshed docs across README and the docs site.

### Fixed
- Progress bar and connection/tool-update issues in the MCP server.
- Slow initial indexing time on large repositories.
- MCP `initialize` response now reports the actual build-time binary version instead of a hardcoded, drifted string.

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
