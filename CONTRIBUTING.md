# Contributing to `devctx`

Thank you for your interest in contributing to `devctx`! We welcome contributions of all kinds: new language AST parsers, MCP tools, bug reports, performance optimizations, and documentation improvements.

---

## 🛠️ Development Setup

### Prerequisites
- **Go 1.21+**: [golang.org/dl](https://golang.org/dl)
- **Git**: [git-scm.com](https://git-scm.com)

### 1. Clone & Build
```bash
# Clone the repository
git clone https://github.com/recscse/devctx.git
cd devctx

# Download dependencies
go mod download

# Build the local binary
go build -o devctx .
```

### 2. Run Tests
Always ensure all tests pass before submitting code:
```bash
# Run unit & integration tests
go test -count=1 -v ./...

# Run Go linter / static analysis
go vet ./...
```

---

## 🧩 Adding a New Language AST Parser

`devctx` is designed to be easily extensible to new programming languages:
1. Navigate to `internal/symbols/`.
2. Implement your language symbol extractor (e.g. `ExtractJavaSymbols` or `ExtractCSharpSymbols`).
3. Add the language mapping to `internal/scanner/scanner.go` in `DetectLanguage()`.
4. Add unit test cases in `internal/symbols/symbols_test.go`.

---

## 📦 Adding a New MCP Tool

To expose a new MCP tool to AI agents:
1. Define the tool JSON schema in `internal/mcp/server.go` under `tools/list`.
2. Add the tool execution logic in `executeTool()`.
3. Add integration test coverage in `internal/mcp/server_test.go`.

---

## 🔀 Pull Request Process

1. **Fork** the repository and create your branch from `main`:
   ```bash
   git checkout -b feat/my-amazing-feature
   ```
2. **Commit** your changes with clear semantic messages:
   ```bash
   git commit -m "feat(symbols): add Kotlin AST symbol extractor"
   ```
3. **Push** to your fork:
   ```bash
   git push origin feat/my-amazing-feature
   ```
4. **Open a Pull Request** against the `main` branch. GitHub Actions will automatically run cross-platform CI tests on Linux, macOS, and Windows.

---

## 📄 License
By contributing to `devctx`, you agree that your contributions will be licensed under the project's [MIT License](LICENSE).
