// Package cmd implements the command line actions and subcommands for ctxd.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/recscse/ctxd/internal/ui"
)

// RunInstallHooks writes post-commit and post-checkout Git hooks to keep the index synchronized automatically.
func RunInstallHooks(targetDir string) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}

	gitHooksDir := filepath.Join(absDir, ".git", "hooks")
	if _, err := os.Stat(filepath.Dir(gitHooksDir)); os.IsNotExist(err) {
		return fmt.Errorf("not a git repository (no .git directory found at %s)", absDir)
	}

	if err := os.MkdirAll(gitHooksDir, 0755); err != nil {
		return fmt.Errorf("failed to create git hooks directory: %w", err)
	}

	hookScript := `#!/bin/sh
# ctxd auto-sync git hook
if command -v ctxd >/dev/null 2>&1; then
    ctxd update >/dev/null 2>&1 &
fi
`

	postCommit := filepath.Join(gitHooksDir, "post-commit")
	postCheckout := filepath.Join(gitHooksDir, "post-checkout")

	if err := os.WriteFile(postCommit, []byte(hookScript), 0755); err != nil {
		return fmt.Errorf("failed to write post-commit hook: %w", err)
	}
	if err := os.WriteFile(postCheckout, []byte(hookScript), 0755); err != nil {
		return fmt.Errorf("failed to write post-checkout hook: %w", err)
	}

	ui.Header("🪝 Git Hooks Installed Successfully")
	fmt.Println()
	fmt.Printf("  %s %s\n", ui.GreenBold.Sprint("✓ Installed:"), ui.Bold.Sprint(postCommit))
	fmt.Printf("  %s %s\n\n", ui.GreenBold.Sprint("✓ Installed:"), ui.Bold.Sprint(postCheckout))
	ui.Success("ctxd will now automatically re-index on every commit and branch checkout.")

	return nil
}
