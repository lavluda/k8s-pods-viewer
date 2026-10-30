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

package client

import (
	"os"
	"path/filepath"
	"testing"
)

const kubeconfigWithContext = `apiVersion: v1
kind: Config
current-context: from-second-file
clusters:
- name: c
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: from-second-file
  context:
    cluster: c
    user: u
users:
- name: u
  user:
    token: t
`

// The kubeconfig flag accepts the same path list KUBECONFIG does. Splitting it
// must use the OS list separator, or a Windows path like C:\... falls apart.
func TestKubeconfigListUsesOSPathListSeparator(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "empty")
	second := filepath.Join(dir, "config")
	if err := os.WriteFile(first, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(kubeconfigWithContext), 0o600); err != nil {
		t.Fatal(err)
	}

	list := first + string(os.PathListSeparator) + second
	if got, want := ResolveContextName(list, ""), "from-second-file"; got != want {
		t.Fatalf("ResolveContextName() = %q, want %q", got, want)
	}
	cfg, err := getConfig(list, "")
	if err != nil {
		t.Fatalf("getConfig() error = %v", err)
	}
	if cfg.Host != "https://127.0.0.1:6443" {
		t.Fatalf("getConfig() host = %q", cfg.Host)
	}
}
