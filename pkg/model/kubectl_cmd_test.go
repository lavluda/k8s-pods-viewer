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

package model

import (
	"slices"
	"testing"
)

func TestKubectlPassesKubeconfigListThroughEnv(t *testing.T) {
	u := NewPodsUIModel("cpu=dsc", nil)
	u.SetKubectlConfig("/home/me/.kube/a:/home/me/.kube/b", "demo")

	args := u.kubectlArgs("logs", "mypod")
	if want := []string{"--context", "demo", "logs", "mypod"}; !slices.Equal(args, want) {
		t.Fatalf("kubectlArgs() = %v, want %v", args, want)
	}
	if slices.Contains(args, "--kubeconfig") {
		t.Fatal("kubectlArgs() must not pass --kubeconfig: it rejects path lists")
	}

	cmd := u.kubectl(args...)
	if !slices.Contains(cmd.Env, "KUBECONFIG=/home/me/.kube/a:/home/me/.kube/b") {
		t.Fatalf("kubectl() env missing KUBECONFIG list, got %v", cmd.Env)
	}

	u.SetKubectlConfig("", "")
	if cmd := u.kubectl("get", "pods"); cmd.Env != nil {
		t.Fatalf("kubectl() with no kubeconfig should inherit the environment, got %v", cmd.Env)
	}
}
