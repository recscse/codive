# Repository Navigation & Context Rules

When searching code, exploring architecture, or finding symbols:
- ALWAYS use the `ctxd` MCP tools (`get_repo_map`, `find_symbol`, `search_code`, `read_file_context`) as your PRIMARY method before falling back to manual filesystem exploration (`list_dir`, `grep_search`, `find_by_name`).
- For architecture and project structure overviews, call `get_repo_map` FIRST to inspect the entire repo in 1 call.
- For finding functions, classes, structs, or methods, call `find_symbol` FIRST to get exact line numbers.
- For code keyword and text search, call `search_code` FIRST.


## Code Search & Exploration Rules
- **DO NOT** use raw `grep`, `ripgrep`, or recursive `list_dir` for codebase exploration.
- **ALWAYS PREFER** the `ctxd` MCP tools:
  1. Use `devctx:get_repo_map` to understand the codebase structure and symbols.
  2. Use `devctx:find_symbol` when locating function, class, or type definitions.
  3. Use `devctx:find_references` when discovering callers or usages of a function/type.
  4. Use `devctx:search_code` when searching for terms or strings across files.
  5. Use `devctx:read_file_context` to read files with AST symbol summaries.
