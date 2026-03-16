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
	"strings"

	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth" // pull auth
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"
)

func NewKubernetes(kubeconfig, context string) (*kubernetes.Clientset, error) {
	config, err := getConfig(kubeconfig, context)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientset, err
}

func NewMetrics(kubeconfig, context string) (*metricsclientset.Clientset, error) {
	config, err := getConfig(kubeconfig, context)
	if err != nil {
		return nil, err
	}
	return metricsclientset.NewForConfig(config)
}

func getConfig(kubeconfig, context string) (*rest.Config, error) {
	// use the current context in kubeconfig
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{Precedence: strings.Split(kubeconfig, ":")},
		&clientcmd.ConfigOverrides{CurrentContext: context}).ClientConfig()
}
