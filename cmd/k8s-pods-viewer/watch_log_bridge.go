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
	goflag "flag"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"github.com/lavluda/k8s-pods-viewer/pkg/model"
)

const (
	watchReconnectStatus = "Kubernetes watch interrupted; reconnecting automatically..."
	apiRetryStatus       = "Kubernetes API unavailable; retrying automatically..."
)

type watchLogBridge struct {
	uiModel *model.PodsUIModel
}

func newWatchLogBridge(uiModel *model.PodsUIModel) *watchLogBridge {
	return &watchLogBridge{uiModel: uiModel}
}

func (b *watchLogBridge) Write(p []byte) (int, error) {
	line := string(p)
	switch {
	case strings.Contains(line, "watch ended with error"):
		b.uiModel.SetTransientStatus(watchReconnectStatus, 8*time.Second)
	case strings.Contains(line, "Failed to watch") || strings.Contains(line, "Failed to list"):
		b.uiModel.SetTransientStatus(apiRetryStatus, 8*time.Second)
	}
	return len(p), nil
}

func configureClientLogging(uiModel *model.PodsUIModel) {
	bridge := newWatchLogBridge(uiModel)

	// klog honors -stderrthreshold (default ERROR) independently of
	// -logtostderr, so without raising it client-go's reflector errors
	// (e.g. "Failed to watch"/"Failed to list") still get written straight
	// to os.Stderr and corrupt the Bubble Tea alt-screen. Route everything
	// through the bridge instead.
	fs := goflag.NewFlagSet("klog", goflag.ContinueOnError)
	klog.InitFlags(fs)
	_ = fs.Set("stderrthreshold", "FATAL")

	klog.LogToStderr(false)
	for _, severity := range []string{"INFO", "WARNING", "ERROR", "FATAL"} {
		klog.SetOutputBySeverity(severity, bridge)
	}
}
