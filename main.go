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
	fmt.Println()
	fmt.Printf("  %s  %s\n\n",
		ui.GreenBold.Sprint("devctx"),
		ui.Dim.Sprintf("%s  ·  Local-first context engine for AI coding agents", Version),
	)

	fmt.Printf("  %s\n\n", ui.Dim.Sprint("Usage:  devctx <command> [path] [flags]"))

	sections := []struct {
		heading  string
		commands [][]string
	}{
		{
			"Index Management",
			[][]string{
				{"init          ", "[path]  ", "Scan and index a repository from scratch"},
				{"reindex       ", "[path]  ", "Clean wipe and rebuild the index"},
				{"update        ", "[path]  ", "Incrementally sync changed files"},
				{"watch         ", "[path]  ", "Auto-sync changes in real-time"},
			},
		},
		{
			"Code Intelligence",
			[][]string{
				{"symbol        ", "<name>  ", "Find symbol definitions across the repo"},
				{"refs          ", "<name>  ", "Find call sites, imports, and usages"},
				{"search        ", "<query> ", "Fast full-text search across indexed files"},
				{"blast         ", "<name>  ", "Blast radius — PR impact & regression scope"},
				{"pack          ", "<query> ", "Build a token-optimized context pack for LLMs"},
				{"map           ", "[path]  ", "Print a structural repository symbol map"},
				{"diff          ", "[path]  ", "AST-aware summary of uncommitted git changes"},
			},
		},
		{
			"Setup & Configuration",
			[][]string{
				{"setup         ", "[path]  ", "Auto-configure MCP for installed AI agents"},
				{"install-hooks ", "[path]  ", "Install Git post-commit & post-checkout hooks"},
				{"init-rules    ", "[path]  ", "Auto-generate AI architecture rules file"},
			},
		},
		{
			"Diagnostics & Info",
			[][]string{
				{"status        ", "[path]  ", "Display index status and language breakdown"},
				{"doctor        ", "[path]  ", "Diagnose index health and agent configurations"},
				{"stats         ", "[path]  ", "Token savings and efficiency metrics"},
				{"decisions     ", "[topic] ", "View recorded architectural decisions"},
				{"logs          ", "[path]  ", "Tail recent log entries"},
			},
		},
		{
			"Runtime",
			[][]string{
				{"web           ", "[path]  ", "Launch interactive architecture network graph"},
				{"serve         ", "[path]  ", "Run the MCP (Model Context Protocol) server"},
				{"upgrade       ", "        ", "Self-update devctx to the latest release"},
				{"version       ", "        ", "Show version and build info"},
				{"about         ", "        ", "Project background, author, and license"},
			},
		},
	}

	for _, sec := range sections {
		fmt.Printf("  %s\n", ui.Dim.Sprint(sec.heading))
		for _, c := range sec.commands {
			fmt.Printf("    %s %s  %s\n",
				ui.Bold.Sprint(c[0]),
				ui.Dim.Sprint(c[1]),
				c[2],
			)
		}
		fmt.Println()
	}

	fmt.Printf("  %s\n", ui.Dim.Sprint("Flags"))
	fmt.Printf("    %s  %s\n", ui.Bold.Sprint("--json     "), "Output results in machine-readable JSON")
	fmt.Printf("    %s  %s\n", ui.Bold.Sprint("--no-color "), "Disable ANSI color output")
	fmt.Printf("    %s  %s\n", ui.Bold.Sprint("--verbose  "), "Stream structured logs to stderr")
	fmt.Println()
	fmt.Printf("  %s\n", ui.Dim.Sprint("Examples"))
	fmt.Printf("    %s\n", ui.Dim.Sprint("devctx setup"))
	fmt.Printf("    %s\n", ui.Dim.Sprint("devctx init ./my-project"))
	fmt.Printf("    %s\n", ui.Dim.Sprint(`devctx search "PaymentProcessor"`))
	fmt.Printf("    %s\n", ui.Dim.Sprint("devctx blast GenerateToken"))
	fmt.Printf("    %s\n", ui.Dim.Sprint("devctx pack auth"))
	fmt.Println()
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

	case "status", "info-status":
		targetDir := "."
		if len(args) >= 2 {
			targetDir = args[1]
		}
		if err := cmd.RunStatus(targetDir, asJSON); err != nil {
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
			ui.Error("usage: devctx blast <symbol> [path]")
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
			ui.Error("usage: devctx symbol <name> [path]")
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
			ui.Error("usage: devctx refs <symbol> [path]")
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
			ui.Error("usage: devctx search <query> [path]")
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
			ui.Error("usage: devctx pack <query> [path]")
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
			fmt.Println()
			fmt.Printf("  %s  %s\n", ui.GreenBold.Sprint("devctx"), Version)
			fmt.Printf("  %-20s  %s\n", ui.Dim.Sprint("Git Commit"), GitCommit)
			fmt.Printf("  %-20s  %s\n", ui.Dim.Sprint("Build Date"), BuildDate)
			fmt.Printf("  %-20s  %s\n", ui.Dim.Sprint("Go Version"), runtime.Version())
			fmt.Printf("  %-20s  %s/%s\n", ui.Dim.Sprint("Platform"), runtime.GOOS, runtime.GOARCH)
			fmt.Println()
		}

	case "help", "--help", "-h":
		printUsage()

	default:
		fmt.Println()
		fmt.Printf("  %s  unknown command: %s\n\n", ui.Red.Sprint("error:"), command)
		fmt.Printf("  Run %s for available commands.\n\n", ui.Bold.Sprint("devctx help"))
		os.Exit(1)
	}
}
