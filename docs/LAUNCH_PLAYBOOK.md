> **Do not publish as written.** Figures in this draft are not backed by a real agent benchmark. Replace them with measured results first.

# 🚀 codive Viral Launch Playbook & Community Distribution

This playbook contains ready-to-publish copy, video storyboards, benchmarks, and community submission templates for launching `codive`.

---

## 📍 Phase 1: Hacker News ("Show HN")

**Title**: 
`Show HN: codive – Local SQLite + AST context engine for AI agents (much smaller output for diffs and large-file outlines)`

**URL**: `https://github.com/recscse/codive`

**Text Post**:
```markdown
Hey HN,

We built codive because we noticed a massive inefficiency in how AI coding assistants (Claude Code, Cursor, Windsurf, Antigravity) explore codebases:

When an agent needs to locate a function or understand an architecture, its default behavior is brute-force `grep` or reading entire 2,000-line files. A single keyword search frequently dumps 300 lines of noise into the context window (burning 3,000–8,000 tokens). Because LLMs re-send the full transcript on every turn, you pay for that noise on every subsequent turn.

`codive` is a single compiled Go binary that runs a local-first Model Context Protocol (MCP) server backed by SQLite (WAL mode):

1. **Sub-millisecond AST Indexing**: Extracts functions, types, and interfaces across Go, TypeScript/JS, Python, and Rust.
2. **Code Skeletonizer (`get_file_skeleton`)**: Strips function bodies to `{ /* L45-L92 */ }`, turning a 5,000-token file into a 50-token skeleton.
3. **One-Shot Feature Packer (`pack_feature_context`)**: Given a keyword like "auth", bundles related data models, endpoints, test suites, and skeletons in 1 single turn (250 tokens vs 8,000 tokens).
4. **Call Graph Impact & Blast Radius (`find_callers`, `blast_radius`)**: Tells the agent what breaks before it modifies a signature.
5. **Real-time Savings Counter (`codive stats`)**: Shows exactly how many tokens and dollars your repo saved.

It requires zero cloud accounts, zero telemetry, and zero Docker containers.

Quick install:
- Mac/Linux: `curl -fsSL https://recscse.github.io/codive/install.sh | bash && codive setup`
- Windows: `irm https://recscse.github.io/codive/install.ps1 | iex; codive setup`

Repo: https://github.com/recscse/codive

We'd love your feedback on the architecture, index format, and multi-language parsers!
```

---

## 🐦 Phase 2: Twitter / X Launch Thread & 15-Second Split Screen

### Tweet 1 (The Hook):
> AI coding assistants read whole files and raw diffs just to find one function. 💸
>
> We built **codive** — an open-source MCP engine that indexes AST symbols in SQLite and answers with just the part that matters.
>
> ⚡ 95% smaller output on git diffs and on big-file outlines (our benchmark, one repo)  
> ⏱️ Simple lookups: about the same as grep  
> 🔒 100% local (zero telemetry)
>
> 👇 Demo & install in thread

### Video Storyboard (15-Second Split-Screen MP4 / GIF):
- **Left Screen (Standard AI)**:
  - Agent runs `list_dir` ➔ `grep_search` ➔ `grep_search` ➔ `view_file`
  - 25 seconds of spinning loading bar, 12,500 tokens burned.
- **Right Screen (codive-Powered Agent)**:
  - Agent runs `codive:pack_feature_context("auth")`
  - 2.1 seconds, 240 tokens, targeted edit applied on Turn 1!
- **Bottom Banner**: `codive setup: Universal 1-Click MCP Auto-Configurator`

### Tweet 2 (Features):
> What makes codive different:
>
> 1️⃣ `pack_feature_context`: 1-shot bundles routes + models + tests  
> 2️⃣ `get_file_skeleton`: Strips function bodies (50-token skeletons)  
> 3️⃣ `blast_radius`: Analyzes what breaks before you refactor  
> 4️⃣ `codive map --web`: Interactive 3D/2D visual architecture graph

### Tweet 3 (Install):
> Try it on your monorepo right now:
>
> ```bash
> # Mac / Linux
> curl -fsSL https://recscse.github.io/codive/install.sh | bash && codive setup
> 
> # Windows
> irm https://recscse.github.io/codive/install.ps1 | iex; codive setup
> ```
> GitHub: https://github.com/recscse/codive

---

## 💬 Phase 3: Reddit Technical Deep Dives

### Subreddit: `r/ClaudeAI` & `r/Cursor`
**Title**: `A local AST + SQLite MCP indexer for coding agents: what we measured (codive)`

**Post**:
> We compared command output sizes on one repository (see docs/BENCHMARK_REPORT.md). We have not yet measured full agent sessions.
> 
> We built `codive` (Go + SQLite + MCP) to act as a structured AST layer:
> - Instead of grep, Claude calls `find_symbol` or `pack_feature_context`.
> - Instead of dumping 2,000 lines of code, Claude calls `get_file_skeleton` to read the signatures and target only lines 45–90.
> - Measured: git diff 36,661 to 1,906 tokens; 2,088-line file outline 16,956 to 755 tokens.
> 
> It's open source and installs with `codive setup`. Detailed benchmarks and architecture: [GitHub link]

### Subreddit: `r/LocalLLaMA`
**Title**: `Making 8B/14B local models code effectively on 50k-line repos without overflowing context limits`

**Post**:
> When using local models (DeepSeek-Coder, Qwen 2.5 Coder, Llama 3) via Continue or Ollama, small context windows (8k–32k) fill up instantly if the agent uses grep.
> 
> `codive` provides token-budgeted repo maps (`token_budget: 2500`) and function skeletons so local models can navigate large codebases without context degradation or hallucinations.

---

## 📦 Phase 4: MCP Registry Submissions

### 1. Anthropic Official MCP Servers & `awesome-mcp-servers`
- **Name**: `codive`
- **Category**: Code Search, Indexing & Development Tools
- **Description**: Fast, local-first AST symbol indexer and Model Context Protocol (MCP) server that returns much smaller context for diffs and large files.
- **Repository**: `https://github.com/recscse/codive`
- **Supported Clients**: Claude Desktop, Cursor, Google Antigravity, VS Code / Continue / Cline, Windsurf.
