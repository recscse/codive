package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/recscse/codive/internal/config"
	"github.com/recscse/codive/internal/scanner"
	"github.com/recscse/codive/internal/ui"
)

// RunConfig shows a project's settings, or reads or changes one of them.
// args are what follows `codive config`: nothing, "get KEY", "set KEY VALUE"
// or "unset KEY". The project is the current directory.
func RunConfig(args []string) error {
	root, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	c, err := config.Load(root)
	if err != nil {
		return err
	}

	switch {
	case len(args) == 0:
		return showConfig(root, c)
	case args[0] == "get" && len(args) == 2:
		v, err := c.Get(args[1])
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
	case args[0] == "set" && len(args) == 3:
		if err := c.Set(args[1], args[2]); err != nil {
			return err
		}
	case args[0] == "unset" && len(args) == 2:
		if err := c.Set(args[1], ""); err != nil {
			return err
		}
	default:
		return fmt.Errorf("usage: codive config [get KEY | set KEY VALUE | unset KEY]")
	}

	if err := config.Save(root, c); err != nil {
		return fmt.Errorf("failed to save %s: %w", config.FileName, err)
	}
	ui.Success(fmt.Sprintf("Saved %s", config.Path(root)))
	return nil
}

func showConfig(root string, c config.Config) error {
	fmt.Println()
	ui.Header("Settings")
	for _, s := range config.Settings {
		value, _ := c.Get(s.Key)
		shown := value
		if shown == "" {
			shown = s.Default + ui.Dim.Sprint(" (default)")
		}
		ui.KeyValue(s.Key, shown)
		fmt.Printf("  %s\n", ui.Dim.Sprint("  "+s.Description))
	}
	fmt.Println()
	fmt.Printf("  Stored in %s\n", config.Path(root))
	fmt.Println("  Change one with: codive config set KEY VALUE")
	fmt.Println()

	ui.Header("Excluded from indexing")
	patterns, err := scanner.LoadIgnorePatterns(root)
	if err != nil {
		return err
	}
	if len(patterns) == 0 {
		fmt.Println("  Nothing beyond .gitignore and built-in rules.")
	}
	for _, p := range patterns {
		fmt.Printf("  %s\n", p)
	}
	fmt.Printf("\n  Add paths to %s, one pattern per line, then run codive reindex.\n\n", filepath.Join(root, ".codiveignore"))
	return nil
}
