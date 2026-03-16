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
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lavluda/k8s-pods-viewer/pkg/client"
	"github.com/lavluda/k8s-pods-viewer/pkg/model"
)

//go:generate cp ../../ATTRIBUTION.md ./ATTRIBUTION.md
//go:embed ATTRIBUTION.md
var attribution string

func main() {
	flags, err := ParseFlags()
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		log.Fatalf("cannot parse flags: %v", err)
	}

	if flags.ShowAttribution {
		fmt.Println(attribution)
		os.Exit(0)
	}

	if flags.Version {
		fmt.Printf("k8s-pods-viewer version %s\n", version)
		fmt.Printf("commit: %s\n", commit)
		fmt.Printf("built at: %s\n", date)
		fmt.Printf("built by: %s\n", builtBy)
		os.Exit(0)
	}

	runtime, err := prepareRuntimeConfig(flags)
	if err != nil {
		log.Fatal(err)
	}

	cs, err := client.NewKubernetes(flags.Kubeconfig, flags.Context)
	if err != nil {
		log.Fatalf("creating client, %s", err)
	}
	metricsClient, err := client.NewMetrics(flags.Kubeconfig, flags.Context)
	if err != nil {
		log.Fatalf("creating metrics client, %s", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := model.NewPodsUIModel(flags.PodSort, runtime.style)
	m.SetResources(runtime.resources)

	controller := client.NewPodsController(cs, metricsClient, m, runtime.nodeSelector, runtime.podSelector, flags.Namespace)
	controller.Start(ctx)

	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		log.Fatalf("error running tea: %s", err)
	}
}
