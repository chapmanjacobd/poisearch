package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateReadme(t *testing.T) {
	tests := []struct {
		name        string
		initial     string
		title       string
		report      string
		wantContent string
	}{
		{
			name:    "header not found - should append new section",
			initial: "# README\n\nSome content\n",
			title:   "Benchmark Results",
			report:  "report line 1\nreport line 2",
			wantContent: "# README\n\nSome content\n\n## Benchmark Results\n```plain\nreport line 1\nreport line 2\n```",
		},
		{
			name:    "header exists with code block - should replace",
			initial: "# README\n\n## Benchmark Results\n```plain\nold report\n```\n\nOther content\n",
			title:   "Benchmark Results",
			report:  "new report",
			wantContent: "# README\n\n## Benchmark Results\n```plain\nnew report\n```\n\nOther content\n",
		},
		{
			name:    "header exists without code block - should add code block",
			initial: "# README\n\n## Benchmark Results\n\nOther content\n",
			title:   "Benchmark Results",
			report:  "new report",
			wantContent: "# README\n\n## Benchmark Results\n```plain\nnew report\n```\n\nOther content\n",
		},
		{
			name: "header exists with multi-line code block - should replace entire block",
			initial: "# README\n\n## Benchmark Results\n```plain\nline 1\nline 2\nline 3\n```\n\nOther content\n",
			title:   "Benchmark Results",
			report:  "replaced",
			wantContent: "# README\n\n## Benchmark Results\n```plain\nreplaced\n```\n\nOther content\n",
		},
		{
			name: "multiple headers - should only update matching one",
			initial: "# README\n\n## Old Results\n```plain\nold\n```\n\n## Benchmark Results\n```plain\nexisting\n```\n\nEnd\n",
			title:   "Benchmark Results",
			report:  "updated",
			wantContent: "# README\n\n## Old Results\n```plain\nold\n```\n\n## Benchmark Results\n```plain\nupdated\n```\n\nEnd\n",
		},
		{
			name: "code block with extra whitespace - should trim and replace",
			initial: "# README\n\n## Benchmark Results\n```plain\n\n  old report with spaces  \n\n```\n\nFooter\n",
			title:   "Benchmark Results",
			report:  "  new report  ",
			wantContent: "# README\n\n## Benchmark Results\n```plain\nnew report\n```\n\nFooter\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory
			tmpDir := t.TempDir()
			readmePath := filepath.Join(tmpDir, "README.md")

			// Write initial content
			err := os.WriteFile(readmePath, []byte(tt.initial), 0o644)
			if err != nil {
				t.Fatalf("failed to write initial README: %v", err)
			}

			// Change to temp dir so updateReadme finds the file
			originalDir, _ := os.Getwd()
			err = os.Chdir(tmpDir)
			if err != nil {
				t.Fatalf("failed to chdir to temp dir: %v", err)
			}
			defer func() {
				err := os.Chdir(originalDir)
				if err != nil {
					t.Fatalf("failed to chdir back: %v", err)
				}
			}()

			// Call updateReadme
			updateReadme(tt.title, tt.report)

			// Read the result
			content, err := os.ReadFile(readmePath)
			if err != nil {
				t.Fatalf("failed to read README after update: %v", err)
			}

			got := string(content)
			if got != tt.wantContent {
				t.Errorf("README content mismatch.\n\nGOT:\n%s\n\nWANT:\n%s\n", got, tt.wantContent)
			}

			// Verify the report is properly trimmed in the code block
			if !strings.Contains(got, "```plain\n"+strings.TrimSpace(tt.report)+"\n```") {
				t.Errorf("Code block does not contain trimmed report.\n\nGOT:\n%s\n", got)
			}
		})
	}
}

func TestUpdateReadme_PreservesOtherContent(t *testing.T) {
	initial := `# My Project

## Introduction

This is a great project.

## Benchmark Results (test.pbf, 100 MB)

` + "```plain" + `
old benchmark data
line 2
line 3
` + "```" + `

## Installation

Run ` + "`make install`" + ` to install.

## Usage

Use the CLI to search.
`

	report := "new benchmark data"
	title := "Benchmark Results (test.pbf, 100 MB)"

	tmpDir := t.TempDir()
	readmePath := filepath.Join(tmpDir, "README.md")

	err := os.WriteFile(readmePath, []byte(initial), 0o644)
	if err != nil {
		t.Fatalf("failed to write README: %v", err)
	}

	originalDir, _ := os.Getwd()
	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	updateReadme(title, report)

	content, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("failed to read README: %v", err)
	}

	got := string(content)

	// Verify other sections are preserved
	if !strings.Contains(got, "## Introduction") {
		t.Error("Introduction section was lost")
	}
	if !strings.Contains(got, "This is a great project.") {
		t.Error("Introduction content was lost")
	}
	if !strings.Contains(got, "## Installation") {
		t.Error("Installation section was lost")
	}
	if !strings.Contains(got, "`make install`") {
		t.Error("Installation content was lost")
	}
	if !strings.Contains(got, "## Usage") {
		t.Error("Usage section was lost")
	}

	// Verify benchmark was updated
	if !strings.Contains(got, "new benchmark data") {
		t.Error("New benchmark data not found")
	}
	if strings.Contains(got, "old benchmark data") {
		t.Error("Old benchmark data still present")
	}
}

func TestUpdateReadme_MultipleUpdates(t *testing.T) {
	initial := "# README\n\n## Benchmark Results\n```plain\nv1\n```\n"
	
	tmpDir := t.TempDir()
	readmePath := filepath.Join(tmpDir, "README.md")

	err := os.WriteFile(readmePath, []byte(initial), 0o644)
	if err != nil {
		t.Fatalf("failed to write README: %v", err)
	}

	originalDir, _ := os.Getwd()
	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	// First update
	updateReadme("Benchmark Results", "v2")
	
	content, _ := os.ReadFile(readmePath)
	if !strings.Contains(string(content), "v2") {
		t.Error("First update failed")
	}

	// Second update
	updateReadme("Benchmark Results", "v3")
	
	content, _ = os.ReadFile(readmePath)
	if !strings.Contains(string(content), "v3") {
		t.Error("Second update failed")
	}
	if strings.Contains(string(content), "v2") {
		t.Error("Old version still present after second update")
	}

	// Third update
	updateReadme("Benchmark Results", "v4")
	
	content, _ = os.ReadFile(readmePath)
	if !strings.Contains(string(content), "v4") {
		t.Error("Third update failed")
	}
	if strings.Contains(string(content), "v3") {
		t.Error("Old version still present after third update")
	}
}
