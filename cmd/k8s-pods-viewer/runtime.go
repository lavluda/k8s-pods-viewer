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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/lavluda/k8s-pods-viewer/pkg/model"
)

type runtimeConfig struct {
	style        *model.Style
	nodeSelector labels.Selector
	podSelector  labels.Selector
	resources    []string
}

func prepareRuntimeConfig(flags Flags) (*runtimeConfig, error) {
	style, err := model.ParseStyle(flags.Style)
	if err != nil {
		return nil, fmt.Errorf("creating style: %w", err)
	}

	nodeSelector, err := labels.Parse(flags.NodeSelector)
	if err != nil {
		return nil, fmt.Errorf("parsing node selector: %w", err)
	}

	podSelector, err := labels.Parse(flags.PodSelector)
	if err != nil {
		return nil, fmt.Errorf("parsing pod selector: %w", err)
	}

	return &runtimeConfig{
		style:        style,
		nodeSelector: nodeSelector,
		podSelector:  podSelector,
		resources: strings.FieldsFunc(flags.Resources, func(r rune) bool {
			return r == ','
		}),
	}, nil
}
