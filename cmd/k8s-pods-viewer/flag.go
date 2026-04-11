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
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"k8s.io/client-go/util/homedir"
)

var (
	homeDir    string
	configPath string
	version    = "dev"
	commit     = ""
	date       = ""
	builtBy    = ""
)

func init() {
	homeDir = homedir.HomeDir()
	configPath = filepath.Join(homeDir, ".k8s-pods-viewer")
}

type Flags struct {
	Context         string
	Kubeconfig      string
	NodeSelector    string
	PodSelector     string
	Namespace       string
	PodSort         string
	Style           string
	Resources       string
	AltScreen       bool
	ShowAttribution bool
	Version         bool
}

func ParseFlags() (Flags, error) {
	flagSet := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	var flags Flags

	cfg, err := loadConfigFile()
	if err != nil {
		return Flags{}, fmt.Errorf("load config file: %w", err)
	}

	flagSet.BoolVar(&flags.Version, "v", false, "Display k8s-pods-viewer version")
	flagSet.BoolVar(&flags.Version, "version", false, "Display k8s-pods-viewer version")

	contextDefault := cfg.getValue("context", "")
	flagSet.StringVar(&flags.Context, "context", contextDefault, "Name of the kubernetes context to use")

	kubeconfigDefault := getStringEnv("KUBECONFIG", cfg.getValue("kubeconfig", filepath.Join(homeDir, ".kube", "config")))
	flagSet.StringVar(&flags.Kubeconfig, "kubeconfig", kubeconfigDefault, "Absolute path to the kubeconfig file")

	nodeSelectorDefault := cfg.getValue("node-selector", "")
	flagSet.StringVar(&flags.NodeSelector, "node-selector", nodeSelectorDefault, "Node label selector used to filter nodes")

	podSelectorDefault := cfg.getValue("pod-selector", "")
	flagSet.StringVar(&flags.PodSelector, "pod-selector", podSelectorDefault, "Pod label selector used to filter pods")

	namespaceDefault := cfg.getValue("namespace", "")
	flagSet.StringVar(&flags.Namespace, "namespace", namespaceDefault, "Namespace to watch; empty means all namespaces")

	podSortDefault := cfg.getValue("pod-sort", "cpu=dsc")
	flagSet.StringVar(&flags.PodSort, "pod-sort", podSortDefault, "Sort order for pods. Examples: cpu=dsc, memory=asc, creation=dsc, namespace=asc")

	resourcesDefault := cfg.getValue("resources", "cpu,memory")
	flagSet.StringVar(&flags.Resources, "resources", resourcesDefault, "List of comma separated resources to monitor")

	styleDefault := cfg.getValue("style", "#04B575,#FFFF00,#FF0000")
	flagSet.StringVar(&flags.Style, "style", styleDefault, "Three colors for styling 'good','ok' and 'bad' values")

	altScreenDefault := cfg.getBool("alt-screen", false)
	flagSet.BoolVar(&flags.AltScreen, "alt-screen", altScreenDefault, "Run in the terminal alternate screen buffer")

	flagSet.BoolVar(&flags.ShowAttribution, "attribution", false, "Show the Open Source Attribution")

	if err := flagSet.Parse(os.Args[1:]); err != nil {
		return Flags{}, err
	}
	return flags, nil
}

func getStringEnv(envName, defaultValue string) string {
	env, ok := os.LookupEnv(envName)
	if !ok {
		return defaultValue
	}
	return env
}

type configFile map[string]string

func (c configFile) getValue(key, defaultValue string) string {
	if val, ok := c[key]; ok {
		return val
	}
	return defaultValue
}

func (c configFile) getBool(key string, defaultValue bool) bool {
	val, ok := c[key]
	if !ok {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(val))
	if err != nil {
		return defaultValue
	}
	return parsed
}

func loadConfigFile() (configFile, error) {
	fileContent := make(map[string]string)
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		return fileContent, nil
	}

	file, err := os.Open(configPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		lineKV := strings.SplitN(line, "=", 2)
		if len(lineKV) == 2 {
			key := strings.TrimSpace(lineKV[0])
			value := strings.TrimSpace(lineKV[1])
			fileContent[key] = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return fileContent, nil
}
