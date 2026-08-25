# Repository Navigation & Context Rules

When searching code, exploring architecture, or finding symbols:
- ALWAYS use the `ctxd` MCP tools (`get_repo_map`, `find_symbol`, `search_code`, `read_file_context`) as your PRIMARY method before falling back to manual filesystem exploration (`list_dir`, `grep_search`, `find_by_name`).
- For architecture and project structure overviews, call `get_repo_map` FIRST to inspect the entire repo in 1 call.
- For finding functions, classes, structs, or methods, call `find_symbol` FIRST to get exact line numbers.
- For code keyword and text search, call `search_code` FIRST.
