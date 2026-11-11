// Package config reads and writes per-project settings in .codive.json at the
// repository root. It lives outside .codive/ so `codive clean` doesn't remove
// it and teams can commit it.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// FileName is the settings file at the repository root.
const FileName = ".codive.json"

// Defaults used when a setting isn't set.
const (
	DefaultPort          = 7890
	DefaultWatchInterval = 2 * time.Second
)

// Config holds project settings. Zero values mean "use the default".
type Config struct {
	Port          int    `json:"port,omitempty"`
	WatchInterval string `json:"watch_interval,omitempty"`
}

// Setting describes one key `codive config` can change.
type Setting struct {
	Key         string
	Description string
	Default     string
}

// Settings lists every supported key, in display order.
var Settings = []Setting{
	{"port", "Port for codive web", strconv.Itoa(DefaultPort)},
	{"watch_interval", "How often codive watch checks for changes, e.g. 2s or 1m", DefaultWatchInterval.String()},
}

// Path returns the settings file for a repository root.
func Path(root string) string {
	return filepath.Join(root, FileName)
}

// Load reads the settings for root. A missing file gives an empty Config.
func Load(root string) (Config, error) {
	var c Config
	data, err := os.ReadFile(Path(root))
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("%s is not valid JSON: %w", FileName, err)
	}
	return c, c.validate()
}

// Save writes the settings for root, removing the file when nothing is set.
func Save(root string, c Config) error {
	if c == (Config{}) {
		err := os.Remove(Path(root))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(root), append(data, '\n'), 0o644)
}

// EffectivePort returns the configured port or the default.
func (c Config) EffectivePort() int {
	if c.Port != 0 {
		return c.Port
	}
	return DefaultPort
}

// EffectiveWatchInterval returns the configured interval or the default.
func (c Config) EffectiveWatchInterval() time.Duration {
	if d, err := time.ParseDuration(c.WatchInterval); err == nil && d > 0 {
		return d
	}
	return DefaultWatchInterval
}

// Get returns a setting's configured value, or "" if it isn't set.
func (c Config) Get(key string) (string, error) {
	switch key {
	case "port":
		if c.Port == 0 {
			return "", nil
		}
		return strconv.Itoa(c.Port), nil
	case "watch_interval":
		return c.WatchInterval, nil
	}
	return "", unknownKey(key)
}

// Set changes one setting after validating it. An empty value unsets it.
func (c *Config) Set(key, value string) error {
	next := *c
	switch key {
	case "port":
		next.Port = 0
		if value != "" {
			p, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("port must be a number: %q", value)
			}
			next.Port = p
		}
	case "watch_interval":
		next.WatchInterval = value
	default:
		return unknownKey(key)
	}
	if err := next.validate(); err != nil {
		return err
	}
	*c = next
	return nil
}

func (c Config) validate() error {
	if c.Port != 0 && (c.Port < 1 || c.Port > 65535) {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}
	if c.WatchInterval != "" {
		d, err := time.ParseDuration(c.WatchInterval)
		if err != nil || d < 500*time.Millisecond || d > time.Hour {
			return fmt.Errorf("watch_interval must be a duration between 500ms and 1h, such as 2s or 1m, got %q", c.WatchInterval)
		}
	}
	return nil
}

func unknownKey(key string) error {
	keys := make([]string, 0, len(Settings))
	for _, s := range Settings {
		keys = append(keys, s.Key)
	}
	sort.Strings(keys)
	return fmt.Errorf("unknown setting %q; available settings: %v", key, keys)
}
