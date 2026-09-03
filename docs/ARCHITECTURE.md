# 🏛️ `codive` Technical Architecture & Design Philosophy

This document provides a deep technical analysis of the internal architecture of `codive`, comparing its design with systems built by Anthropic (Model Context Protocol), Cursor (Symbol Graph & Indexing), Aider (Tree-sitter Repo Map), and Sourcegraph (SCIP / LSIF).

---

## 1. The Core Problem: Why LLM Context Exploration Fails

When an AI agent (e.g. Claude 3.5 Sonnet, GPT-4o, DeepSeek-Coder) interacts with a software repository, traditional exploration tools rely on two primitive primitives:
1. **Raw File Tree Lists (`list_dir` / `ls`)**: Dumps raw directory paths without semantic meaning.
2. **Text-based Grep (`grep` / `ripgrep`)**: Performs substring or regex matching across all files.

### The Math Behind Token Incineration
Consider an agent attempting to refactor an authentication function `GenerateToken` across a 40,000-line repository:
1. **Turn 1**: The agent runs `grep -rn "GenerateToken" .`.
2. **Output**: Returns 180 matching lines, including test fixtures, mock data, build logs, and comments (≈ 3,500 tokens).
3. **Turn 2**: The agent reads 3 full candidate files (≈ 9,000 tokens).
4. **Turn 3**: The agent modifies the signature, but misses callers in middleware files (fails tests).
5. **Turn 4–7**: Repeated grep and file-read retry loops.

Because LLMs are stateless across turns, **the entire conversation history is re-sent on every single turn**:
$$\text{Total Tokens} = \sum_{i=1}^{N} \left( \text{Prompt}_i + \sum_{k=1}^{i-1} \text{TurnOutput}_k \right)$$
In a 10-turn debugging session, burning 8,000 tokens on Turn 1 means you pay for those 8,000 noisy tokens **10 separate times** (80,000 compound tokens wasted on noise).

---

## 2. Industry Architecture Comparison

| Dimension | **`codive`** | **Anthropic MCP Model** | **Cursor Indexer** | **Aider Repo Map** |
| :--- | :--- | :--- | :--- | :--- |
| **Storage Engine** | **SQLite (WAL Mode) + FTS5** | Protocol Standard (No fixed DB) | Cloud Merkle Trees & Local Cache | In-Memory NetworkX Graph |
| **Parsing Mechanism** | **Multi-Language AST Engine** | Delegated to Server Implementations | Tree-sitter + LSP | Tree-sitter AST |
| **Code Skeletonizer** | **Native (`get_file_skeleton`)** | Client Discretion | Proprietary Chunking | Syntax Tag Summaries |
| **Call Graph / Blast** | **Native (`find_callers`, `blast`)** | Standard Call Protocol | Proprietary AI Ranking | PageRank over References |
| **Telemetry / ROI** | **Real-Time Token Counter (`codive stats`)** | None | Usage Metering | Token Counter |
| **Privacy** | **100% Local-First (Zero Cloud)** | Depends on MCP Server | Cloud Indexing Optional | 100% Local |

---

## 3. High-Level System Architecture

```mermaid
graph TB
    subgraph AI Clients Layer
        C1["Claude Desktop"]
        C2["Cursor IDE"]
        C3["Google Antigravity"]
        C4["VS Code / Continue"]
        C5["Windsurf"]
    end

    subgraph MCP Transport Layer
        RPC["JSON-RPC 2.0 (stdio) Protocol Handler"]
    end

    subgraph Core Engine
        ROUTER["14-Tool MCP Router & Dispatcher"]
        DRIFT["Line-Number Drift Protection Engine"]
        SKELETON["Code Skeletonizer Engine"]
        BLAST["PR Blast Radius Impact Engine"]
        FTS["Symbol-Boosted FTS5 Search"]
        PACK["1-Shot Feature Pack Builder"]
    end

    subgraph Storage Layer (Local-First)
        DB[(".codive/index.db<br/>SQLite in WAL Mode")]
        T_FILES[("files Table")]
        T_SYMS[("symbols Table")]
        T_FTS[("file_fts (FTS5 Table)")]
        T_DEC[("decisions Table (Persistent Memory)")]
        T_TEL[("telemetry Table (Savings Tracker)")]
    end

    C1 & C2 & C3 & C4 & C5 -->|stdio JSON-RPC| RPC
    RPC --> ROUTER
    ROUTER --> DRIFT & SKELETON & BLAST & FTS & PACK
    DRIFT & SKELETON & BLAST & FTS & PACK --> DB
    DB --> T_FILES & T_SYMS & T_FTS & T_DEC & T_TEL
```

---

## 4. Key Subsystem Deep-Dives

### A. Sub-Millisecond AST Extraction
`codive` extracts declarations (functions, structs, interfaces, classes, methods) using dedicated deterministic lexical AST extractors:
- **Go**: `go/parser` and `go/ast` extracting package names, receivers, structs, interfaces, and function signatures.
- **TypeScript / JavaScript**: Multi-pass regex and token-based parser extracting `export function`, `export class`, `interface`, `type`, `const handler = () => {}`.
- **Python**: Class and `def` extractors with decorator association and type annotations.
- **Rust**: `struct`, `enum`, `trait`, `impl`, and `fn` extractors.

### B. SQLite WAL Mode Storage & PRAGMA Optimization
`codive` configures SQLite for zero-lock concurrency:
```sql
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 5000;
PRAGMA user_version = 5;
```
- **Read Latency**: `< 0.2ms` per symbol lookup.
- **Write Latency**: `< 5ms` during incremental updates.

### C. Symbol-Boosted Full-Text Search (FTS5)
Standard FTS5 matches strings anywhere in file contents. `codive` combines FTS5 with symbol ranking:
1. Performs BM25 keyword search across `file_fts`.
2. Computes an AST boost: If a file *declares* a symbol with the search term, its score is boosted by $3\times$.
3. Emits clean snippet highlights using `[` and `]` delimiters.

### D. Line-Number Drift Protection
When an AI agent modifies a file in the editor, the database index might have slightly older line numbers. `codive` intercepts `read_file_context`, `get_file_skeleton`, and `find_symbol`:
1. Compares the file's disk modified timestamp (`mtime`) with `files.last_indexed`.
2. If $mtime > last\_indexed$, it triggers a synchronous **micro-reparse** in $<2\text{ms}$.
3. Guarantees line numbers passed to the AI are always 100% exact.

---

## 5. Security & Privacy Guarantees

1. **Zero External Network Calls**: `codive` operates strictly on `localhost` via standard I/O pipes or local loopback HTTP (`127.0.0.1:7890`).
2. **Zero Cloud Telemetry**: All token savings statistics remain strictly inside your local `.codive/index.db`.
3. **Repository Sandboxing**: All file paths are strictly validated against repository boundaries, preventing path traversal attacks.
