package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/recscse/codive/internal/db"
	"github.com/recscse/codive/internal/ranker"
	"github.com/recscse/codive/internal/ui"
)

// RankerReport is what `codive ranker` shows: the feedback collected for
// read_symbol intent ranking and the model trained from it.
type RankerReport struct {
	LearningEnabled bool               `json:"learning_enabled"`
	Feedback        db.LearningStats   `json:"feedback"`
	Model           ranker.Model       `json:"model"`
	Weights         map[string]float64 `json:"weights"`
	ColdStart       map[string]float64 `json:"cold_start_weights"`
	NeededToTrain   int                `json:"queries_needed_before_training"`
}

// RunRanker prints (action "status") or forgets (action "reset") what the
// intent ranker has learned in targetDir's index.
func RunRanker(targetDir, action string, asJSON bool) error {
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("invalid directory path: %w", err)
	}
	dbPath := filepath.Join(absDir, ".codive", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("repository is not initialized (no index found at %s). Run 'codive init' first", dbPath)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open index database: %w", err)
	}
	defer database.Close()
	ctx := context.Background()

	switch action {
	case "reset":
		if err := db.ResetLearning(ctx, database, ranker.MetaKey); err != nil {
			return err
		}
		ui.Success("Forgot all intent feedback; read_symbol intent ranking is back to its starting weights.")
		return nil
	case "", "status":
	default:
		return fmt.Errorf("unknown ranker action %q (use status or reset)", action)
	}

	stats, err := db.GetLearningStats(ctx, database)
	if err != nil {
		return err
	}
	model := ranker.DefaultModel()
	if raw, err := db.GetMeta(ctx, database, ranker.MetaKey); err == nil && raw != "" {
		if err := json.Unmarshal([]byte(raw), &model); err != nil {
			return fmt.Errorf("stored ranker is unreadable (run 'codive ranker reset'): %w", err)
		}
	}
	report := RankerReport{
		LearningEnabled: ranker.Enabled(),
		Feedback:        stats,
		Model:           model,
		Weights:         named(model.Weights),
		ColdStart:       named(ranker.ColdStart),
		NeededToTrain:   max(0, ranker.MinExamples-stats.Labelled),
	}
	if asJSON {
		return ui.PrintJSON(report)
	}

	ui.SectionHeader("Intent Ranking (read_symbol intent)")
	if report.LearningEnabled {
		ui.KeyValue("Learning", "on (set CODIVE_LEARNING=off to disable)")
	} else {
		ui.KeyValue("Learning", "off (CODIVE_LEARNING=off)")
	}
	ui.KeyValue("Queries logged", fmt.Sprintf("%d (%d with a known useful result)", stats.Logged, stats.Labelled))
	ui.KeyValue("Definitions used", fmt.Sprintf("%d, %s in total", stats.UsedSyms, ui.Count(stats.TotalUses, "use", "uses")))
	if model.Version == 0 {
		msg := "starting weights (word-match ranking)"
		if report.NeededToTrain > 0 {
			msg += fmt.Sprintf("; training starts after %d more useful-result queries", report.NeededToTrain)
		}
		ui.KeyValue("Model", msg)
	} else {
		ui.KeyValueAccent("Model", fmt.Sprintf("version %d, trained on %d queries, %s",
			model.Version, model.TrainedOn, model.UpdatedAt.Local().Format("2006-01-02 15:04")))
		ui.KeyValueAccent("Held-out MRR", fmt.Sprintf("%.2f (starting weights: %.2f)", model.HoldoutMRR, model.BaselineMRR))
	}
	ui.SubHeader("Weights")
	for i, name := range ranker.FeatureNames {
		ui.KeyValue(name, fmt.Sprintf("%+.2f  (start %+.2f)", model.Weights[i], ranker.ColdStart[i]))
	}
	fmt.Println()
	return nil
}

func named(w ranker.Weights) map[string]float64 {
	out := make(map[string]float64, len(w))
	for i, name := range ranker.FeatureNames {
		out[name] = w[i]
	}
	return out
}
