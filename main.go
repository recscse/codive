package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/recscse/devctx/internal/cmd"
	"github.com/recscse/devctx/internal/logger"
	"github.com/recscse/devctx/internal/ui"
)

// Build metadata injected via -ldflags during build
var (
	Version   = "v1.0.0"
	GitCommit = "none"
	BuildDate = "2026-08-25"
)

func printUsage() {
	ui.Header(fmt.Sprintf("devctx %s — Developer Context Engine for AI Coding Agents", Version))
	ui.Divider()
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  devctx <command> [arguments] [flags]")
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  init          [path]      Scan and index a repository from scratch")
	fmt.Println("  init-rules    [path]      Auto-generate customized AI architecture rules")
	fmt.Println("  install-hooks [path]      Install Git post-commit & post-checkout hooks")
	fmt.Println("  reindex       [path]      Clean wipe and rebuild index from scratch")
	fmt.Println("  update        [path]      Incrementally synchronize repository index")
	fmt.Println("  status        [path]      Display index status and language breakdown")
	fmt.Println("  doctor        [path]      Diagnose index health and AI agent configurations")
	fmt.Println("  diff          [path]      AST-aware summary of uncommitted git changes")
	fmt.Println("  decisions     [topic]     View recorded architectural memory & decisions")
	fmt.Println("  stats         [path]      Real-time token & cloud money savings counter")
	fmt.Println("  blast         <name>      PR Blast Radius impact & regression analyzer")
	fmt.Println("  web           [path]      Launch interactive web architecture network graph")
	fmt.Println("  map           [path]      Generate a structural repository symbol map")
	fmt.Println("  symbol        <name>      Find symbol definitions across the repository")
	fmt.Println("  refs          <name>      Find call sites, imports, and usages of a symbol")
	fmt.Println("  search        <query>     Fast full-text code search across indexed files")
	fmt.Println("  pack          <query>     Build a token-optimized context pack for LLMs")
	fmt.Println("  watch         [path]      Watch repository and auto-sync changes in real-time")
	fmt.Println("  setup         [path]      Auto-configure devctx for installed AI agents")
	fmt.Println("  upgrade                   Self-update devctx binary to latest release")
	fmt.Println("  about                     Show project background, author, and license")
	fmt.Println("  logs          [path]      Show recent log entries from .devctx/devctx.log")
	fmt.Println("  serve         [path]      Run the Model Context Protocol (MCP) server")
	fmt.Println("  version                   Show detailed version and build information")
	fmt.Println("  help                      Show this help message")
	fmt.Println("\nFlags:")
	fmt.Println("  --json                    Output results in JSON format")
	fmt.Println("  --no-color                Disable ANSI color styling")
	fmt.Println("  --verbose                 Output structured logs to stderr in real-time")
	fmt.Println("\nExamples:")
	fmt.Println("  devctx setup")
	fmt.Println("  devctx init")
	fmt.Println("  devctx search \"PaymentProcessor\"")
	fmt.Println("  devctx blast GenerateToken")
	fmt.Println("  devctx pack auth")
	fmt.Println("  devctx map --web")
	fmt.Println("  devctx upgrade")
	fmt.Println("  devctx about")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	// Filter global flags
	var args []string
	asJSON := false
	noColor := false
	verbose := false

	for _, arg := range os.Args[1:] {
		switch arg {
		case "--no-color":
			noColor = true
		case "--json":
			asJSON = true
		case "--verbose":
			verbose = true
		default:
			args = append(args, arg)
		}
	}

	if noColor {
		ui.SetNoColor(true)
	}

	if len(args) == 0 {
		printUsage()
		os.Exit(0)
	}

	command := strings.ToLower(args[0])

	// Determine working dir for logger
	logTargetDir := "."
	if len(args) >= 2 && !strings.HasPrefix(args[1], "-") {
		// If command is "symbol", "search", or "pack", directory is args[2] if present
		if (command == "symbol" || command == "find-symbol" || command == "search" || command == "pack") && len(args) >= 3 {
			logTargetDir = args[2]
		} else if command != "symbol" && command != "find-symbol" && command != "search" && command != "pack" {
			logTargetDir = args[1]
		}
	}

	// Initialize structured logging
	if command != "help" && command != "--help" && command != "-h" && command != "version" && command != "--version" && command != "-v" {
		_ = logger.InitLogger(logTargetDir, verbose)
		defer logger.Close()
	}

	switch command {
	case "init":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunInit(targetDir); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "init-rules", "rules":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunInitRules(targetDir); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "install-hooks", "hooks":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunInstallHooks(targetDir); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "decisions":
		topic := ""
		if len(args) >= 2 {
			topic = args[1]
		}
		targetDir := "."
		if len(args) >= 3 {
			targetDir = args[2]
		}
		if err := cmd.RunDecisions(targetDir, topic, asJSON); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "reindex":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunReindex(targetDir); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "doctor":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunDoctor(targetDir); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "diff", "changes":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunChanges(targetDir, asJSON); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "update":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunUpdate(targetDir); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "stats", "savings":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunStats(targetDir, asJSON); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "blast", "blast-radius":
		if len(args) < 2 {
			ui.Error("Usage: ctxd blast <symbol> [path] [--json]")
			os.Exit(1)
		}
		symbol := args[1]
		targetDir := "."
		if len(args) >= 3 {
			targetDir = args[2]
		}
		if err := cmd.RunBlast(targetDir, symbol, asJSON); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "web":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunMapWeb(targetDir, 7890); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "map":
		targetDir := "."
		if len(args) >= 2 {
			if args[1] == "--web" || args[1] == "-web" {
				if err := cmd.RunMapWeb(targetDir, 7890); err != nil {
					ui.Error(err.Error())
					os.Exit(1)
				}
				return
			}
			targetDir = args[1]
		}
		if err := cmd.RunMap(targetDir, asJSON); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "symbol", "find-symbol":
		if len(args) < 2 {
			ui.Error("Usage: ctxd symbol <name> [path] [--json]")
			os.Exit(1)
		}
		query := args[1]
		targetDir := "."
		if len(args) >= 3 {
			targetDir = args[2]
		}
		if err := cmd.RunFindSymbol(targetDir, query, asJSON); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "refs", "references", "find-references":
		if len(args) < 2 {
			ui.Error("Usage: ctxd refs <symbol> [path] [--json]")
			os.Exit(1)
		}
		symbol := args[1]
		targetDir := "."
		if len(args) >= 3 {
			targetDir = args[2]
		}
		if err := cmd.RunRefs(targetDir, symbol, 30, asJSON); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "search":
		if len(args) < 2 {
			ui.Error("Usage: ctxd search <query> [path] [--json]")
			os.Exit(1)
		}
		query := args[1]
		targetDir := "."
		if len(args) >= 3 {
			targetDir = args[2]
		}
		if err := cmd.RunSearch(targetDir, query, 20, asJSON); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "pack":
		if len(args) < 2 {
			ui.Error("Usage: ctxd pack <query> [path]")
			os.Exit(1)
		}
		query := args[1]
		targetDir := "."
		if len(args) >= 3 {
			targetDir = args[2]
		}
		if err := cmd.RunPack(targetDir, query); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "watch":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunWatch(targetDir, 0); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "setup":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunSetup(targetDir); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "logs":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunLogs(targetDir, 50); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "serve":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunServe(targetDir); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "upgrade", "self-update":
		if err := cmd.RunUpgrade(Version); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "about", "info":
		if err := cmd.RunAbout(Version, BuildDate, GitCommit); err != nil {
			ui.Error(err.Error())
			os.Exit(1)
		}

	case "version", "--version", "-v":
		if asJSON {
			_ = ui.PrintJSON(map[string]string{
				"version":    Version,
				"git_commit": GitCommit,
				"build_date": BuildDate,
				"go_version": runtime.Version(),
				"platform":   fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
			})
		} else {
			ui.Header(fmt.Sprintf("devctx %s", Version))
			fmt.Printf("  %s   %s\n", ui.Dim.Sprint("Git Commit:"), GitCommit)
			fmt.Printf("  %s   %s\n", ui.Dim.Sprint("Build Date:"), BuildDate)
			fmt.Printf("  %s   %s\n", ui.Dim.Sprint("Go Version:"), runtime.Version())
			fmt.Printf("  %s   %s/%s\n", ui.Dim.Sprint("Platform:  "), runtime.GOOS, runtime.GOARCH)
		}

	case "help", "--help", "-h":
		printUsage()

	default:
		ui.Error(fmt.Sprintf("Unknown command: %s\n", command))
		printUsage()
		os.Exit(1)
	}
}
