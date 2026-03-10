package audio

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Source represents a PipeWire audio capture source.
type Source struct {
	ID          int
	Name        string
	Description string
	score       int
}

// ListSources enumerates PipeWire audio capture sources via pw-dump.
// Monitor (loopback) sources are excluded.
// Results are sorted by heuristic score: USB > Bluetooth > Digital/DMIC > Analog.
func ListSources() ([]Source, error) {
	out, err := exec.Command("pw-dump").Output()
	if err != nil {
		return nil, fmt.Errorf("pw-dump: %w (is PipeWire running?)", err)
	}

	var objects []pwObject
	if err := json.Unmarshal(out, &objects); err != nil {
		return nil, fmt.Errorf("parse pw-dump: %w", err)
	}

	var sources []Source
	for _, o := range objects {
		if o.Type != "PipeWire:Interface:Node" {
			continue
		}
		props := o.Info.Props
		if strProp(props, "media.class") != "Audio/Source" {
			continue
		}

		name := strProp(props, "node.name")
		desc := strProp(props, "node.description")

		if strings.Contains(name, "monitor") {
			continue
		}

		s := Source{
			ID:          o.ID,
			Name:        name,
			Description: desc,
			score:       scoreMic(name, desc),
		}
		sources = append(sources, s)
	}

	if len(sources) == 0 {
		return nil, fmt.Errorf("no audio capture sources found")
	}

	sort.Slice(sources, func(i, j int) bool {
		return sources[i].score > sources[j].score
	})

	return sources, nil
}

// FindSource picks a source. If deviceHint is empty, returns the
// highest-scored source. Otherwise matches by ID or substring of
// name/description.
func FindSource(sources []Source, deviceHint string) (*Source, error) {
	if deviceHint == "" {
		return &sources[0], nil
	}

	for i := range sources {
		idStr := fmt.Sprintf("%d", sources[i].ID)
		if idStr == deviceHint {
			return &sources[i], nil
		}
		if strings.Contains(strings.ToLower(sources[i].Name), strings.ToLower(deviceHint)) ||
			strings.Contains(strings.ToLower(sources[i].Description), strings.ToLower(deviceHint)) {
			return &sources[i], nil
		}
	}

	return nil, fmt.Errorf("no source matching %q", deviceHint)
}

func scoreMic(name, desc string) int {
	low := strings.ToLower(name + " " + desc)
	switch {
	case strings.Contains(low, "usb"):
		return 100
	case strings.Contains(low, "bluez") || strings.Contains(low, "bluetooth"):
		return 90
	case strings.Contains(low, "dmic") || strings.Contains(low, "pdm") || strings.Contains(low, "digital mic"):
		return 80
	default:
		return 50
	}
}

func strProp(props map[string]any, key string) string {
	v, _ := props[key].(string)
	return v
}

type pwObject struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Info pwInfo `json:"info"`
}

type pwInfo struct {
	Props map[string]any `json:"props"`
}
