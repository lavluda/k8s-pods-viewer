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
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
)

type DensityMode string

const (
	DensityComfortable DensityMode = "comfortable"
	DensityCompact     DensityMode = "compact"
)

type BarStyleMode string

const (
	BarStyleSegmented BarStyleMode = "segmented"
	BarStyleSolid     BarStyleMode = "solid"
	BarStyleGradient  BarStyleMode = "gradient"
)

type Style struct {
	green     func(strs ...string) string
	yellow    func(strs ...string) string
	red       func(strs ...string) string
	greenHex  string
	yellowHex string
	redHex    string
	gradient  progress.Option

	accentHex string
	density   DensityMode
	barStyle  BarStyleMode
}

func ParseStyle(style string) (*Style, error) {
	colors := strings.Split(style, ",")
	if len(colors) != 3 {
		return nil, fmt.Errorf("three colors must be provided for the style, found %d (%q)", len(colors), style)
	}
	s := &Style{}
	s.greenHex = colors[0]
	s.yellowHex = colors[1]
	s.redHex = colors[2]
	s.green = lipgloss.NewStyle().Foreground(lipgloss.Color(colors[0])).Render
	s.yellow = lipgloss.NewStyle().Foreground(lipgloss.Color(colors[1])).Render
	s.red = lipgloss.NewStyle().Foreground(lipgloss.Color(colors[2])).Render

	s.gradient = progress.WithGradient(colors[2], colors[0])

	s.accentHex = "#39D353"
	s.density = DensityComfortable
	s.barStyle = BarStyleSegmented
	return s, nil
}

func (s *Style) SetAccent(hex string) error {
	hex = strings.TrimSpace(hex)
	if hex == "" {
		return nil
	}
	if !looksLikeHexColor(hex) {
		return fmt.Errorf("accent must be a #RRGGBB color, got %q", hex)
	}
	s.accentHex = hex
	return nil
}

func (s *Style) SetDensity(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "comfortable":
		s.density = DensityComfortable
	case "compact":
		s.density = DensityCompact
	default:
		return fmt.Errorf("density must be comfortable or compact, got %q", mode)
	}
	return nil
}

func (s *Style) SetBarStyle(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "segmented":
		s.barStyle = BarStyleSegmented
	case "solid":
		s.barStyle = BarStyleSolid
	case "gradient":
		s.barStyle = BarStyleGradient
	default:
		return fmt.Errorf("bar-style must be segmented, solid, or gradient, got %q", mode)
	}
	return nil
}

func (s *Style) Accent() lipgloss.Color { return lipgloss.Color(s.accentHex) }
func (s *Style) Density() DensityMode   { return s.density }
func (s *Style) BarStyle() BarStyleMode { return s.barStyle }

func (s *Style) SeverityColor(severity PodHealthSeverity) lipgloss.Color {
	switch severity {
	case PodHealthCritical:
		return lipgloss.Color(s.redHex)
	case PodHealthWarning:
		return lipgloss.Color(s.yellowHex)
	default:
		return lipgloss.Color(s.greenHex)
	}
}

func looksLikeHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, r := range s[1:] {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
