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
	"strings"
	"testing"

	"github.com/lavluda/k8s-pods-viewer/pkg/model"
)

func TestWatchLogBridgeShowsReconnectStatus(t *testing.T) {
	style, err := model.ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := model.NewPodsUIModel("creation=dsc", style)
	bridge := newWatchLogBridge(uiModel)

	if _, err := bridge.Write([]byte(`W0317 10:39:29.618676 reflector.go:578] "Warning: watch ended with error" err="unable to decode an event from the watch stream: http2: client connection lost"`)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if got := uiModel.View(); !strings.Contains(got, watchReconnectStatus) {
		t.Fatalf("View() missing reconnect status, got %q", got)
	}
}

func TestWatchLogBridgeShowsAPIRetryStatus(t *testing.T) {
	style, err := model.ParseStyle("#04B575,#FFFF00,#FF0000")
	if err != nil {
		t.Fatalf("ParseStyle() error = %v", err)
	}

	uiModel := model.NewPodsUIModel("creation=dsc", style)
	bridge := newWatchLogBridge(uiModel)

	if _, err := bridge.Write([]byte(`E0317 10:39:29.618676 reflector.go:324] pkg/mod/...: Failed to list *v1.Pod: Get "https://cluster.example/api/v1/pods": dial tcp: i/o timeout`)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if got := uiModel.View(); !strings.Contains(got, apiRetryStatus) {
		t.Fatalf("View() missing API retry status, got %q", got)
	}
}
