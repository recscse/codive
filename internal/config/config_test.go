package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestDefaultsWithoutFile(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.EffectivePort() != DefaultPort || c.EffectiveWatchInterval() != DefaultWatchInterval {
		t.Errorf("unexpected defaults: %d %v", c.EffectivePort(), c.EffectiveWatchInterval())
	}
}

func TestSetSaveLoadAndUnset(t *testing.T) {
	root := t.TempDir()
	var c Config
	if err := c.Set("port", "8080"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("watch_interval", "5s"); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.EffectivePort() != 8080 || loaded.EffectiveWatchInterval() != 5*time.Second {
		t.Errorf("round trip lost values: %+v", loaded)
	}

	if err := loaded.Set("port", ""); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Set("watch_interval", ""); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path(root)); !os.IsNotExist(err) {
		t.Errorf("unsetting everything should remove the file, got %v", err)
	}
}

func TestRejectsBadValues(t *testing.T) {
	var c Config
	for key, value := range map[string]string{"port": "70000", "watch_interval": "10ms", "nope": "1"} {
		if err := c.Set(key, value); err == nil {
			t.Errorf("Set(%q, %q) should fail", key, value)
		}
	}
	if err := c.Set("port", "abc"); err == nil || !strings.Contains(err.Error(), "number") {
		t.Errorf("non-numeric port should fail clearly, got %v", err)
	}
	if c != (Config{}) {
		t.Errorf("failed Set must not change the config: %+v", c)
	}

	root := t.TempDir()
	if err := os.WriteFile(Path(root), []byte(`{"port": 0.5`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Errorf("invalid JSON should fail")
	}
}
