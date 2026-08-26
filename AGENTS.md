# AI Agent Architecture & Exploration Guidelines

## Project Overview
- **Primary Language**: Go
- **Build Command**: `go build ./...`
- **Test Command**: `go test -count=1 -v ./...`
- **Lint Command**: `go vet ./...`

## Code Search & Exploration Rules
- **DO NOT** use raw `grep`, `ripgrep`, or recursive `list_dir` for codebase exploration.
- **ALWAYS PREFER** the `devctx` MCP tools for zero-token code discovery:
  1. Use `devctx:get_repo_map` to understand the codebase structure and symbols.
  2. Use `devctx:get_file_skeleton` to inspect file structure without dumping thousands of tokens.
  3. Use `devctx:find_symbol` when locating function, class, or type definitions.
  4. Use `devctx:find_callers` or `devctx:find_references` when discovering usages or refactoring.
  5. Use `devctx:find_tests_for` to locate corresponding unit test suites before and after making changes.
  6. Use `devctx:pack_feature_context` to bundle complete feature entrypoints in 1 single turn.
  7. Use `devctx:get_git_changes` to review uncommitted AST changes without noisy unified diffs.

## Engineering Conventions
1. Always run tests using the verified test command after making edits.
2. Avoid breaking existing exported signatures without updating all callers.
3. Record significant architectural decisions using `devctx:save_decision`.
