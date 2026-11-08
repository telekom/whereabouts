// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
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

// TestDependabotCooldown pins the cooldown policy: every version updater waits
// 3 days, and SemVer-major bumps wait 14 days where the ecosystem supports the
// semver-* options. Security updates ignore cooldown by design.
func TestDependabotCooldown(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatalf("reading dependabot config: %v", err)
	}
	var config struct {
		Updates []struct {
			Ecosystem string         `json:"package-ecosystem"`
			Cooldown  map[string]int `json:"cooldown"`
		} `json:"updates"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parsing dependabot config: %v", err)
	}
	if len(config.Updates) == 0 {
		t.Fatal("dependabot config has no updates entries")
	}
	// Ecosystems whose cooldown supports semver-major-days per the Dependabot options reference.
	semver := map[string]bool{"gomod": true, "npm": true}
	for _, u := range config.Updates {
		if got := u.Cooldown["default-days"]; got != 3 {
			t.Errorf("%s: cooldown.default-days = %d, want 3", u.Ecosystem, got)
		}
		want := 0
		if semver[u.Ecosystem] {
			want = 14
		}
		if got := u.Cooldown["semver-major-days"]; got != want {
			t.Errorf("%s: cooldown.semver-major-days = %d, want %d", u.Ecosystem, got, want)
		}
	}
}

// TestDependabotGrouping pins the grouping policy: every Go module updater
// collects its security updates in one PR, and Actions keep majors apart from
// minor/patch bumps.
func TestDependabotGrouping(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatalf("reading dependabot config: %v", err)
	}
	var config struct {
		Updates []struct {
			Ecosystem string `json:"package-ecosystem"`
			Groups    map[string]struct {
				AppliesTo   string   `json:"applies-to"`
				Patterns    []string `json:"patterns"`
				UpdateTypes []string `json:"update-types"`
			} `json:"groups"`
		} `json:"updates"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parsing dependabot config: %v", err)
	}
	for _, u := range config.Updates {
		security := 0
		types := map[string]bool{}
		for _, g := range u.Groups {
			if g.AppliesTo == "security-updates" {
				security++
				if strings.Join(g.Patterns, ",") != "*" {
					t.Errorf("%s security group patterns = %v, want [*]", u.Ecosystem, g.Patterns)
				}
			}
			types[strings.Join(g.UpdateTypes, ",")] = true
		}
		switch u.Ecosystem {
		case "gomod":
			if security != 1 {
				t.Errorf("gomod updater has %d security-updates groups, want 1", security)
			}
		case "github-actions":
			if !types["major"] || !types["minor,patch"] {
				t.Errorf("github-actions updater must group major and minor/patch separately")
			}
		}
	}
}
