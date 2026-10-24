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
        ROUTER["15-Tool MCP Router & Dispatcher"]
        DRIFT["Line-Number Drift Protection Engine"]
        SKELETON["Code Skeletonizer Engine"]
        BLAST["PR Blast Radius Impact Engine"]
        FTS["Symbol-Boosted FTS5 Search"]
        PACK["1-Shot Feature Pack Builder (Attention U-Curve)"]
        TREE["Proportional Tree Allocator"]
        INTENT["Subword Intent Decomposition Engine"]
        SESSION["Session Working-Set Deduplicator"]
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

### E. Proportional Tree Budgeting
Traditional repository mappers sort directories alphabetically and perform a greedy cutoff when token limits are reached, resulting in late-alphabet packages (e.g. `services/`, `utils/`, `web/`) being completely dropped. `codive` implements a two-pass round-robin proportional allocator:
1. **Pass 1 (Breadth Guarantee)**: Allocates base quota to every top-level package to guarantee its existence and top entrypoint files are visible.
2. **Pass 2 (Depth Allotment)**: Distributes remaining character capacity across directories proportionally based on symbol count, gracefully summarizing omitted files (`... +N more files in <dir>`) without dropping any module.

### F. Subword Intent Decomposition & Attention U-Curve
To bridge the gap between human/agent intent and codebase naming conventions:
1. **Subword Decomposition**: Lexically splits camelCase, PascalCase, snake_case, and kebab-case identifiers into component tokens (`VerifyCredentials` $\rightarrow$ `verify`, `credentials`).
2. **Domain Intent Clustering & Trigram Vectors**: Maps intent keywords (`login`, `throttle`, `db`) to domain synonyms (`auth`, `rate limit`, `store`) with character 3-gram Dice coefficient vector scoring.
3. **Attention U-Curve Formatting**: In accordance with Transformer self-attention dynamics (solving "Lost in the Middle"), context is partitioned into high-attention zones: Core Types & Interfaces at the top (priming anchor), structural skeletons in the middle, and target implementation functions & unit test assertions at the very bottom (recency anchor).

### G. Session Working-Set Deduplication
In multi-turn agent pair programming sessions:
1. `SessionTracker` maintains an in-memory record of files and symbols delivered over the lifetime of the active MCP connection.
2. On subsequent turns, re-reading the same skeleton or symbol returns a compact reference anchor (`// [Symbol 'X' already delivered in Turn #N]`), saving 3,000–8,000 tokens per turn while preserving model awareness.
3. Agents can bypass deduplication at any time by supplying `force: true`.

---

## 5. Security & Privacy Guarantees

1. **Zero External Network Calls**: `codive` operates strictly on `localhost` via standard I/O pipes or local loopback HTTP (`127.0.0.1:7890`).
2. **Zero Cloud Telemetry**: All token savings statistics remain strictly inside your local `.codive/index.db`.
3. **Repository Sandboxing**: All file paths are strictly validated against repository boundaries, preventing path traversal attacks.
