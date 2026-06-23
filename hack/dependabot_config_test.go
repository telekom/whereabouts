package hack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependabotGitHubActionsUsesSingleRootUpdater(t *testing.T) {
	config, err := os.ReadFile(filepath.Join("..", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatalf("reading dependabot config: %v", err)
	}

	text := string(config)
	if got := strings.Count(text, "package-ecosystem: github-actions"); got != 1 {
		t.Fatalf("expected exactly one github-actions updater, got %d", got)
	}
	if !strings.Contains(text, "package-ecosystem: github-actions\n    directory: /") {
		t.Fatalf("github-actions updater must use root directory")
	}
	if strings.Contains(text, "directory: /.github/workflows") {
		t.Fatalf("github-actions updater must not use non-root workflow directory")
	}
}
