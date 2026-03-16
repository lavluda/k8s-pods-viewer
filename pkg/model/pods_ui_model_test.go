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
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestFormatResourceQuantity(t *testing.T) {
	tests := []struct {
		name     string
		resource v1.ResourceName
		quantity string
		want     string
	}{
		{name: "cpu cores", resource: v1.ResourceCPU, quantity: "2", want: "2"},
		{name: "cpu millicores", resource: v1.ResourceCPU, quantity: "500m", want: "500m"},
		{name: "cpu nanocores", resource: v1.ResourceCPU, quantity: "4302645n", want: "5m"},
		{name: "memory unchanged", resource: v1.ResourceMemory, quantity: "512Mi", want: "512Mi"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatResourceQuantity(tc.resource, resource.MustParse(tc.quantity))
			if got != tc.want {
				t.Fatalf("formatResourceQuantity(%s, %s) = %q, want %q", tc.resource, tc.quantity, got, tc.want)
			}
		})
	}
}
