package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanListsAndDeletesOnlyTheIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".codive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".codive", "index.db"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	rememberProject(project)
	rememberProject(project)

	projects, err := readProjects()
	if err != nil || len(projects) != 1 {
		t.Fatalf("expected one recorded project, got %v, %v", projects, err)
	}

	if err := RunClean(project, false, strings.NewReader("n\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, ".codive")); err != nil {
		t.Fatalf("answering no must keep the index: %v", err)
	}

	if err := RunClean(project, false, strings.NewReader("y\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, ".codive")); !os.IsNotExist(err) {
		t.Fatalf("index should be deleted, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "main.go")); err != nil {
		t.Fatalf("project code must not be touched: %v", err)
	}
	if projects, _ := readProjects(); len(projects) != 0 {
		t.Errorf("deleted project should be forgotten, got %v", projects)
	}

	if err := RunClean(project, true, nil); err == nil {
		t.Errorf("cleaning a directory without an index should fail")
	}
}
