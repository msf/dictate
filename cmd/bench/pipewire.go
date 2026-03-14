package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// virtualSource uses pw-loopback to create a linked Audio/Sink + Audio/Source
// pair. pw-cat plays into the sink; SDL2 (whisper-stream) captures from the source.
type virtualSource struct {
	sinkName     string
	sourceName   string
	sourceNodeID int
	loopbackCmd  *exec.Cmd
}

func createVirtualSource() (*virtualSource, error) {
	pid := os.Getpid()
	sinkName := fmt.Sprintf("bench_sink_%d", pid)
	sourceName := fmt.Sprintf("bench_mic_%d", pid)

	cmd := exec.Command("pw-loopback",
		fmt.Sprintf("--capture-props=media.class=Audio/Sink node.name=%s node.description=%s", sinkName, sinkName),
		fmt.Sprintf("--playback-props=media.class=Audio/Source node.name=%s node.description=%s", sourceName, sourceName),
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("pw-loopback: %w", err)
	}

	// Wait for PipeWire to register the nodes.
	time.Sleep(500 * time.Millisecond)

	sourceID, err := findNodeID(sourceName, "Audio/Source")
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}

	return &virtualSource{
		sinkName:     sinkName,
		sourceName:   sourceName,
		sourceNodeID: sourceID,
		loopbackCmd:  cmd,
	}, nil
}

func (vs *virtualSource) cleanup() {
	if vs.loopbackCmd != nil && vs.loopbackCmd.Process != nil {
		_ = vs.loopbackCmd.Process.Kill()
		_ = vs.loopbackCmd.Wait()
	}
}

func findNodeID(nodeName, mediaClass string) (int, error) {
	out, err := exec.Command("pw-dump").Output()
	if err != nil {
		return 0, fmt.Errorf("pw-dump: %w", err)
	}

	var objects []struct {
		ID   int    `json:"id"`
		Type string `json:"type"`
		Info struct {
			Props map[string]any `json:"props"`
		} `json:"info"`
	}
	if err := json.Unmarshal(out, &objects); err != nil {
		return 0, fmt.Errorf("parse pw-dump: %w", err)
	}

	for _, o := range objects {
		if o.Type != "PipeWire:Interface:Node" {
			continue
		}
		props := o.Info.Props
		nn, _ := props["node.name"].(string)
		mc, _ := props["media.class"].(string)
		if nn == nodeName && mc == mediaClass {
			return o.ID, nil
		}
	}

	return 0, fmt.Errorf("node %q (%s) not found in pw-dump", nodeName, mediaClass)
}

// rewireCapture disconnects all current links to a capture node's input ports,
// then connects the source node's output ports to them.
// Needed because PIPEWIRE_NODE hint is unreliable for virtual loopback sources.
func rewireCapture(captureNode, sourceNode string) error {
	out, err := exec.Command("pw-link", "-lI").Output()
	if err != nil {
		return fmt.Errorf("pw-link -lI: %w", err)
	}

	var currentOutput string
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if m := pwLinkPortRe.FindStringSubmatch(line); m != nil {
			currentOutput = m[1]
			continue
		}
		if m := pwLinkEdgeRe.FindStringSubmatch(line); m != nil {
			inputPort := m[1]
			if strings.HasPrefix(inputPort, captureNode+":") && currentOutput != "" {
				fmt.Fprintf(os.Stderr, "bench: disconnect %s -> %s\n", currentOutput, inputPort)
				_ = exec.Command("pw-link", "-d", currentOutput, inputPort).Run()
			}
		}
	}

	outPorts, err := listPorts("pw-link", "-o", sourceNode)
	if err != nil {
		return err
	}
	inPorts, err := listPorts("pw-link", "-i", captureNode)
	if err != nil {
		return err
	}

	n := min(len(inPorts), len(outPorts))
	for i := 0; i < n; i++ {
		fmt.Fprintf(os.Stderr, "bench: link %s -> %s\n", outPorts[i], inPorts[i])
		if err := exec.Command("pw-link", outPorts[i], inPorts[i]).Run(); err != nil {
			return fmt.Errorf("pw-link %s %s: %w", outPorts[i], inPorts[i], err)
		}
	}

	if n == 0 {
		return fmt.Errorf("no ports to link between %s and %s", sourceNode, captureNode)
	}
	return nil
}

func listPorts(tool, flag, nodePrefix string) ([]string, error) {
	out, err := exec.Command(tool, flag).Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", tool, flag, err)
	}
	var ports []string
	for line := range strings.SplitSeq(string(out), "\n") {
		port := strings.TrimSpace(line)
		if strings.HasPrefix(port, nodePrefix+":") {
			ports = append(ports, port)
		}
	}
	return ports, nil
}
