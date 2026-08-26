// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/recscse/devctx/internal/db"
	"github.com/recscse/devctx/internal/ui"
)

// BlastRadiusResult represents the impact analysis of changing a symbol or signature.
type BlastRadiusResult struct {
	Symbol       string   `json:"symbol"`
	RiskLevel    string   `json:"risk_level"` // "HIGH", "MEDIUM", "LOW"
	CallSites    int      `json:"call_sites"`
	AffectedFiles []string `json:"affected_files"`
	TestsToRun   []string `json:"tests_to_run"`
}

// RunBlast analyzes the blast radius and potential regressions when modifying a symbol.
func RunBlast(targetDir string, targetSymbol string, asJSON bool) error {
	if strings.TrimSpace(targetSymbol) == "" {
		return fmt.Errorf("target symbol cannot be empty")
	}

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	dbPath := filepath.Join(absDir, ".devctx", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("repository is not initialized (no index found at %s). Run 'ctxd init' first", dbPath)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()

	ctx := context.Background()
	result, err := AnalyzeBlastRadius(ctx, database, targetSymbol)
	if err != nil {
		return err
	}

	if asJSON {
		return ui.PrintJSON(result)
	}

	var riskBadge string
	switch result.RiskLevel {
	case "HIGH":
		riskBadge = ui.Red.Sprint("⚠️  HIGH IMPACT")
	case "MEDIUM":
		riskBadge = ui.Yellow.Sprint("⚡ MEDIUM IMPACT")
	default:
		riskBadge = ui.GreenBold.Sprint("✓ LOW IMPACT")
	}

	fmt.Println()
	ui.CyanBold.Printf("💥 Blast Radius Analysis for '%s':\n", ui.Bold.Sprint(result.Symbol))
	fmt.Printf("  ├── Risk Assessment:   %s (%d direct callers across %d files)\n",
		riskBadge, result.CallSites, len(result.AffectedFiles))

	fmt.Printf("  ├── 📦 Affected Files:   %s\n", strings.Join(result.AffectedFiles, ", "))

	if len(result.TestsToRun) > 0 {
		fmt.Printf("  └── 🧪 Tests to Run:     %s\n", strings.Join(result.TestsToRun, ", "))
	} else {
		fmt.Printf("  └── 🧪 Tests to Run:     %s\n", ui.Dim.Sprint("No dedicated unit test suites detected"))
	}
	fmt.Println()

	return nil
}

// AnalyzeBlastRadius performs call graph and test suite impact analysis for a symbol.
func AnalyzeBlastRadius(ctx context.Context, database *sql.DB, symbol string) (*BlastRadiusResult, error) {
	// Strip file prefix if passed like "auth.go:GenerateToken"
	cleanSymbol := symbol
	if strings.Contains(symbol, ":") {
		parts := strings.Split(symbol, ":")
		cleanSymbol = parts[len(parts)-1]
	}

	refs, err := db.FindReferences(ctx, database, cleanSymbol, 50)
	if err != nil {
		return nil, err
	}

	fileMap := make(map[string]bool)
	for _, r := range refs {
		fileMap[r.FilePath] = true
	}

	var affectedFiles []string
	for f := range fileMap {
		affectedFiles = append(affectedFiles, f)
	}

	tests, _ := db.FindTestsFor(ctx, database, cleanSymbol)
	var testsToRun []string
	for _, t := range tests {
		testsToRun = append(testsToRun, t.TestFilePath)
		for _, name := range t.TestNames {
			testsToRun = append(testsToRun, name)
		}
	}

	riskLevel := "LOW"
	if len(affectedFiles) >= 3 || len(refs) >= 5 {
		riskLevel = "HIGH"
	} else if len(affectedFiles) >= 2 || len(refs) >= 2 {
		riskLevel = "MEDIUM"
	}

	return &BlastRadiusResult{
		Symbol:        cleanSymbol,
		RiskLevel:     riskLevel,
		CallSites:     len(refs),
		AffectedFiles: affectedFiles,
		TestsToRun:    testsToRun,
	}, nil
}
