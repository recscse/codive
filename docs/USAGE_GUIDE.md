# 📖 `ctxd` Complete User & Integration Guide

This guide covers everything you need to know about setting up, configuring, and using `ctxd` with any AI coding assistant.

---

## 1. Quick Setup by AI Client

### A. Automatic Setup (Recommended)
In your terminal, navigate to your repository root and run:
```bash
ctxd setup
```
`ctxd setup` automatically scans and registers `ctxd` in:
- **Google Antigravity**: `~/.gemini/config/mcp_config.json`
- **Claude Desktop**: `~/Library/Application Support/Claude/claude_desktop_config.json` or `%APPDATA%\Claude\claude_desktop_config.json`
- **Cursor IDE**: `~/.cursor/mcp.json`
- **VS Code / Continue / Cline**: `~/.continue/config.json`
- **Windsurf / Zed**: `.windsurfrules` & local configurations

---

### B. Manual MCP Configuration (Optional)
If you prefer adding the configuration manually to your editor's MCP config:

```json
{
  "mcpServers": {
    "ctxd": {
      "command": "ctxd",
      "args": ["serve"],
      "autoApprove": [
        "pack_feature_context",
        "blast_radius",
        "get_repo_map",
        "get_file_skeleton",
        "find_symbol",
        "find_callers",
        "find_callees",
        "find_tests_for",
        "get_git_changes",
        "save_decision",
        "get_decisions",
        "search_code",
        "read_file_context",
        "find_references"
      ]
    }
  }
}
```

---

## 2. The 14 MCP Tools Reference & Usage

### 1. `pack_feature_context` (One-Shot Feature Discovery)
- **Description**: Bundles related routes, data models, schemas, skeletons, and test suites for a feature keyword into a single compressed context summary.
- **When to use**: Whenever starting work on a feature (e.g. "Add JWT token refresh", "Implement database migrations").
- **Example Agent Prompt**:
  > *"Use `pack_feature_context` on 'auth' to understand how authentication and tokens are handled."*

### 2. `get_file_skeleton` (Code Skeletonizer)
- **Description**: Strips out function and method bodies and preserves type definitions, imports, interfaces, and function signatures with line ranges (`/* L45-L92 */`).
- **When to use**: Before reading large files (500+ lines) to avoid burning 5,000+ tokens.
- **Example Agent Prompt**:
  > *"Inspect the skeleton of `internal/scanner/scanner.go` using `get_file_skeleton`."*

### 3. `blast_radius` (PR Impact & Regression Analyzer)
- **Description**: Analyzes all direct callers, affected files, and unit test suites across the repository before refactoring a symbol.
- **When to use**: Before modifying any exported function, type, or API contract.
- **Example Agent Prompt**:
  > *"Check the `blast_radius` of `ScanIncremental` before we refactor its signature."*

### 4. `find_symbol` (Precise Symbol Resolution)
- **Description**: Finds exact declaration line numbers, kinds (`function`, `struct`, `class`, `interface`), and signatures.
- **When to use**: Finding where a function or struct is declared.
- **Example Agent Prompt**:
  > *"Locate `StartServer` using `find_symbol`."*

### 5. `find_callers` (Call Graph Upstream Analysis)
- **Description**: Discovers all functions, files, and line numbers where a target symbol is invoked.
- **When to use**: Understanding who relies on a function before editing it.

### 6. `find_callees` (Call Graph Downstream Analysis)
- **Description**: Discovers internal functions and types called inside a target function's implementation.

### 7. `find_tests_for` (Smart Test Suite Locator)
- **Description**: Finds the corresponding test file (`*_test.go`, `test_*.py`, `*.spec.ts`) and specific test functions for a source file or symbol.

### 8. `get_repo_map` (Structural Architecture Map)
- **Description**: Emits an indentation-based directory hierarchy annotated with declared symbol signatures, respecting a strict token budget (`token_budget: 3000`).

### 9. `get_git_changes` (AST Diff Summarizer)
- **Description**: Summarizes uncommitted git changes by mapping modified line ranges to enclosing AST functions/classes instead of dumping raw diffs.

### 10. `save_decision` (Persistent Agent Memory)
- **Description**: Stores durable architectural invariants, design decisions, and tricky bug fixes in the persistent SQLite database.
- **Example Call**:
  ```json
  {"topic": "database", "summary": "Always use WAL mode and set busy_timeout to 5000ms"}
  ```

### 11. `get_decisions` (Memory Retrieval)
- **Description**: Retrieves past architectural decisions recorded by previous AI sessions.

### 12. `search_code` (Symbol-Boosted FTS5)
- **Description**: Fast BM25 keyword search with snippet highlights and symbol-declaration score boosting.

### 13. `read_file_context` (Verified File Content)
- **Description**: Reads file contents with Line-Number Drift Protection and symbol outline annotations.

### 14. `find_references` (Repo-Wide Usages)
- **Description**: Finds all references, imports, and usages of a symbol across the entire repository.

---

## 3. CLI Command Guide

| Command | Action | Example |
| :--- | :--- | :--- |
| `ctxd setup` | Auto-configures MCP in installed AI clients | `ctxd setup` |
| `ctxd init` | Scans and indexes the repository | `ctxd init` |
| `ctxd update` | Fast incremental synchronization (<5ms) | `ctxd update` |
| `ctxd stats` | Displays the token and cloud money savings box | `ctxd stats` |
| `ctxd blast <sym>` | Evaluates refactoring blast radius | `ctxd blast GenerateToken` |
| `ctxd map --web` | Launches the interactive browser network graph | `ctxd map --web` |
| `ctxd doctor` | Verifies SQLite integrity and MCP setup | `ctxd doctor` |
| `ctxd diff` | AST-aware summary of uncommitted git changes | `ctxd diff` |
| `ctxd init-rules` | Auto-generates `AGENTS.md` and `.cursorrules` | `ctxd init-rules` |
| `ctxd install-hooks`| Installs Git post-commit & post-checkout hooks | `ctxd install-hooks` |
