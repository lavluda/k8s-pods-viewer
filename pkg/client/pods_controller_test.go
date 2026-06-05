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
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/lavluda/k8s-pods-viewer/pkg/model"
)

func TestSeedNodesForbiddenEnablesPodOnlyMode(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	kubeClient.PrependReactor("list", "nodes", forbiddenReactor("nodes"))
	uiModel := newTestUIModel(t)
	controller := NewPodsController(kubeClient, nil, uiModel, labels.Everything(), labels.Everything(), "production")

	if controller.seedNodes(context.Background(), uiModel.Cluster()) {
		t.Fatal("seedNodes() = true, want false for forbidden node access")
	}
	if uiModel.Cluster().NodeDataAvailable() {
		t.Fatal("NodeDataAvailable() = true, want false")
	}
	if got := uiModel.View(); !strings.Contains(got, "pod-only mode") {
		t.Fatalf("View() missing pod-only mode status, got %q", got)
	}
}

func TestSeedPodsForbiddenStopsPodWatchStartup(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	kubeClient.PrependReactor("list", "pods", forbiddenReactor("pods"))
	uiModel := newTestUIModel(t)
	controller := NewPodsController(kubeClient, nil, uiModel, labels.Everything(), labels.Everything(), "production")

	if controller.seedPods(context.Background(), uiModel.Cluster()) {
		t.Fatal("seedPods() = true, want false for forbidden pod access")
	}
	if got := uiModel.View(); !strings.Contains(got, `Pod access forbidden for namespace "production"`) {
		t.Fatalf("View() missing namespace-scoped RBAC guidance, got %q", got)
	}
}

func TestCanWatchPodsForbiddenReturnsFalse(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	kubeClient.PrependWatchReactor("pods", func(action ktesting.Action) (bool, watch.Interface, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", nil)
	})
	uiModel := newTestUIModel(t)
	controller := NewPodsController(kubeClient, nil, uiModel, labels.Everything(), labels.Everything(), "production")

	if controller.canWatchPods(context.Background()) {
		t.Fatal("canWatchPods() = true, want false for forbidden pod watch")
	}
	if got := uiModel.View(); !strings.Contains(got, "Pod watch forbidden") {
		t.Fatalf("View() missing pod watch RBAC guidance, got %q", got)
	}
}

func forbiddenReactor(resource string) ktesting.ReactionFunc {
	return func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "", nil)
	}
}

func newTestUIModel(t *testing.T) *model.PodsUIModel {
	t.Helper()
	style, err := model.ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}
	return model.NewPodsUIModel("name=asc", style)
}
