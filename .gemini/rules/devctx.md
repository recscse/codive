## Code Search & Exploration Rules
- **DO NOT** use raw `grep`, `ripgrep`, or recursive `list_dir` for codebase exploration.
- **ALWAYS PREFER** the `ctxd` MCP tools:
  1. Use `devctx:get_repo_map` to understand the codebase structure and symbols.
  2. Use `devctx:find_symbol` when locating function, class, or type definitions.
  3. Use `devctx:find_references` when discovering callers or usages of a function/type.
  4. Use `devctx:search_code` when searching for terms or strings across files.
  5. Use `devctx:read_file_context` to read files with AST symbol summaries.
