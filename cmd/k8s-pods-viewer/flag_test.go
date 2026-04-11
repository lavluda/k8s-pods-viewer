/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	originalValue, hadValue := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%q): %v", key, err)
	}
	t.Cleanup(func() {
		var err error
		if hadValue {
			err = os.Setenv(key, originalValue)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil {
			t.Fatalf("restoring env %q: %v", key, err)
		}
	})
}

func withTestConfigPath(t *testing.T, configBody string) string {
	t.Helper()

	tempDir := t.TempDir()
	originalHomeDir := homeDir
	originalConfigPath := configPath
	homeDir = tempDir
	configPath = filepath.Join(tempDir, ".k8s-pods-viewer")
	t.Cleanup(func() {
		homeDir = originalHomeDir
		configPath = originalConfigPath
	})

	if configBody != "" {
		if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
			t.Fatalf("write config file: %v", err)
		}
	}
	return tempDir
}

func withArgs(t *testing.T, args ...string) {
	t.Helper()
	originalArgs := os.Args
	os.Args = append([]string{"k8s-pods-viewer"}, args...)
	t.Cleanup(func() {
		os.Args = originalArgs
	})
}

func TestParseFlagsDefaults(t *testing.T) {
	tempDir := withTestConfigPath(t, "")
	unsetEnv(t, "KUBECONFIG")
	withArgs(t)

	flags, err := ParseFlags()
	if err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}

	if got, want := flags.Kubeconfig, filepath.Join(tempDir, ".kube", "config"); got != want {
		t.Fatalf("Kubeconfig = %q, want %q", got, want)
	}
	if got, want := flags.PodSort, "cpu=dsc"; got != want {
		t.Fatalf("PodSort = %q, want %q", got, want)
	}
	if got, want := flags.Resources, "cpu,memory"; got != want {
		t.Fatalf("Resources = %q, want %q", got, want)
	}
	if got, want := flags.Style, "#04B575,#FFFF00,#FF0000"; got != want {
		t.Fatalf("Style = %q, want %q", got, want)
	}
	if flags.AltScreen {
		t.Fatalf("AltScreen = true, want false")
	}
}

func TestParseFlagsConfigFile(t *testing.T) {
	withTestConfigPath(t, strings.Join([]string{
		"# comment",
		"context = prod-cluster",
		"kubeconfig = /tmp/from-config",
		"namespace = production",
		"node-selector = role=worker",
		"pod-selector = app=api",
		"pod-sort = memory=asc",
		"resources = cpu,memory,ephemeral-storage",
		"style = #111111,#222222,#333333",
		"alt-screen = true",
	}, "\n"))
	unsetEnv(t, "KUBECONFIG")
	withArgs(t)

	flags, err := ParseFlags()
	if err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}

	if got, want := flags.Context, "prod-cluster"; got != want {
		t.Fatalf("Context = %q, want %q", got, want)
	}
	if got, want := flags.Kubeconfig, "/tmp/from-config"; got != want {
		t.Fatalf("Kubeconfig = %q, want %q", got, want)
	}
	if got, want := flags.Namespace, "production"; got != want {
		t.Fatalf("Namespace = %q, want %q", got, want)
	}
	if got, want := flags.NodeSelector, "role=worker"; got != want {
		t.Fatalf("NodeSelector = %q, want %q", got, want)
	}
	if got, want := flags.PodSelector, "app=api"; got != want {
		t.Fatalf("PodSelector = %q, want %q", got, want)
	}
	if got, want := flags.PodSort, "memory=asc"; got != want {
		t.Fatalf("PodSort = %q, want %q", got, want)
	}
	if got, want := flags.Resources, "cpu,memory,ephemeral-storage"; got != want {
		t.Fatalf("Resources = %q, want %q", got, want)
	}
	if got, want := flags.Style, "#111111,#222222,#333333"; got != want {
		t.Fatalf("Style = %q, want %q", got, want)
	}
	if !flags.AltScreen {
		t.Fatalf("AltScreen = false, want true")
	}
}

func TestParseFlagsKubeconfigEnvOverridesConfig(t *testing.T) {
	withTestConfigPath(t, "kubeconfig=/tmp/from-config\n")
	t.Setenv("KUBECONFIG", "/tmp/from-env")
	withArgs(t)

	flags, err := ParseFlags()
	if err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}

	if got, want := flags.Kubeconfig, "/tmp/from-env"; got != want {
		t.Fatalf("Kubeconfig = %q, want %q", got, want)
	}
}

func TestParseFlagsCLIOverridesConfigAndEnv(t *testing.T) {
	withTestConfigPath(t, strings.Join([]string{
		"context=from-config",
		"kubeconfig=/tmp/from-config",
		"namespace=from-config",
		"node-selector=role=worker",
		"pod-selector=app=api",
		"pod-sort=memory=asc",
		"resources=cpu",
		"style=#111111,#222222,#333333",
	}, "\n"))
	t.Setenv("KUBECONFIG", "/tmp/from-env")
	withArgs(t,
		"--context", "from-cli",
		"--kubeconfig", "/tmp/from-cli",
		"--namespace", "from-cli",
		"--node-selector", "topology.kubernetes.io/zone=az1",
		"--pod-selector", "component=web",
		"--pod-sort", "creation=dsc",
		"--resources", "memory,cpu",
		"--style", "#abcdef,#123456,#654321",
		"--alt-screen",
		"--attribution",
	)

	flags, err := ParseFlags()
	if err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}

	if got, want := flags.Context, "from-cli"; got != want {
		t.Fatalf("Context = %q, want %q", got, want)
	}
	if got, want := flags.Kubeconfig, "/tmp/from-cli"; got != want {
		t.Fatalf("Kubeconfig = %q, want %q", got, want)
	}
	if got, want := flags.Namespace, "from-cli"; got != want {
		t.Fatalf("Namespace = %q, want %q", got, want)
	}
	if got, want := flags.NodeSelector, "topology.kubernetes.io/zone=az1"; got != want {
		t.Fatalf("NodeSelector = %q, want %q", got, want)
	}
	if got, want := flags.PodSelector, "component=web"; got != want {
		t.Fatalf("PodSelector = %q, want %q", got, want)
	}
	if got, want := flags.PodSort, "creation=dsc"; got != want {
		t.Fatalf("PodSort = %q, want %q", got, want)
	}
	if got, want := flags.Resources, "memory,cpu"; got != want {
		t.Fatalf("Resources = %q, want %q", got, want)
	}
	if got, want := flags.Style, "#abcdef,#123456,#654321"; got != want {
		t.Fatalf("Style = %q, want %q", got, want)
	}
	if !flags.AltScreen {
		t.Fatalf("AltScreen = false, want true")
	}
	if !flags.ShowAttribution {
		t.Fatalf("ShowAttribution = false, want true")
	}
}

func TestParseFlagsVersionAliases(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "short", args: []string{"-v"}},
		{name: "long", args: []string{"-version"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withTestConfigPath(t, "")
			withArgs(t, tc.args...)

			flags, err := ParseFlags()
			if err != nil {
				t.Fatalf("ParseFlags() error = %v", err)
			}
			if !flags.Version {
				t.Fatalf("Version = false, want true")
			}
		})
	}
}

func TestParseFlagsUnknownFlag(t *testing.T) {
	withTestConfigPath(t, "")
	withArgs(t, "--not-a-real-flag")

	_, err := ParseFlags()
	if err == nil {
		t.Fatalf("ParseFlags() error = nil, want non-nil")
	}
}

func TestLoadConfigFileMissing(t *testing.T) {
	withTestConfigPath(t, "")

	cfg, err := loadConfigFile()
	if err != nil {
		t.Fatalf("loadConfigFile() error = %v", err)
	}
	if len(cfg) != 0 {
		t.Fatalf("len(cfg) = %d, want 0", len(cfg))
	}
}
