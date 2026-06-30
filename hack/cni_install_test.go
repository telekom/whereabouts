// Copyright 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package hack_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/yaml"
)

type cniInstallConfigMap struct {
	Kind string            `json:"kind"`
	Data map[string]string `json:"data"`
}

func TestCNIInstallScriptFailsWhenDownloadFails(t *testing.T) {
	script := cniInstallScript(t)
	if !strings.Contains(script, "set -eu") {
		t.Fatalf("CNI install script must fail fast with set -eu")
	}
	if !strings.Contains(script, `wget -O "${archive}"`) {
		t.Fatalf("CNI install script must write downloads to an explicit archive path")
	}

	hostCNIBin := t.TempDir()
	script = strings.ReplaceAll(script, "/host/opt/cni/bin", hostCNIBin)
	scriptPath := filepath.Join(t.TempDir(), "install_cni.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write rewritten install script: %v", err)
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "wget"), "#!/bin/sh\necho wget failed >&2\nexit 7\n")
	writeExecutable(t, filepath.Join(binDir, "tar"), "#!/bin/sh\necho tar should not run >&2\nexit 8\n")
	writeExecutable(t, filepath.Join(binDir, "tail"), "#!/bin/sh\necho tail should not run >&2\nexit 9\n")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", scriptPath)
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("CNI install script hung after failed download:\n%s", output)
	}
	if err == nil {
		t.Fatalf("CNI install script succeeded after failed download:\n%s", output)
	}
	if strings.Contains(string(output), "tar should not run") || strings.Contains(string(output), "tail should not run") {
		t.Fatalf("CNI install script continued after failed download:\n%s", output)
	}
}

func cniInstallScript(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "hack", "cni-install.yml"))
	if err != nil {
		t.Fatalf("read cni-install.yml: %v", err)
	}
	docs := bytes.Split(data, []byte("\n---"))
	for _, doc := range docs {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}
		jsonDoc, err := yaml.ToJSON(doc)
		if err != nil {
			t.Fatalf("convert CNI install manifest to JSON: %v", err)
		}
		var manifest cniInstallConfigMap
		if err := json.Unmarshal(jsonDoc, &manifest); err != nil {
			t.Fatalf("decode CNI install manifest: %v", err)
		}
		if manifest.Kind != "ConfigMap" {
			continue
		}
		script := manifest.Data["install_cni.sh"]
		if script == "" {
			t.Fatalf("CNI install ConfigMap missing install_cni.sh")
		}
		return script
	}
	t.Fatalf("CNI install ConfigMap not found")
	return ""
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
