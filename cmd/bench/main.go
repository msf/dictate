package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

type benchConfig struct {
	Model      string
	Lang       string
	Step       int
	Length     int
	Keep       int
	AC         int
	CPUOnly    bool
	DictateBin string
}

type corpus struct {
	Name      string
	WAVPath   string
	Reference string
}

type result struct {
	Config     benchConfig
	Corpus     string
	Hypothesis string
	Reference  string
	WER        float64
	EncodeMS   float64
	HeadroomMS float64
	ForcedStop bool
	Duration   time.Duration
}

var (
	timestampRe    = regexp.MustCompile(`^\[\d{2}:\d{2}\.\d Δ[\d.]+s\] `)
	encodeTimingRe = regexp.MustCompile(`whisper_print_timings:\s+encode time =\s+([\d.]+) ms /\s+\d+ runs \(\s+([\d.]+) ms per run\)`)
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("bench: ")

	modelPath := flag.String("model", "", "whisper model path (required)")
	corpusDir := flag.String("corpus", "bench/corpus", "corpus directory with .wav + .txt pairs")
	dictateBin := flag.String("dictate", "bin/dictate", "path to dictate binary")
	lang := flag.String("lang", "en", "language code")
	steps := flag.String("steps", "2500", "comma-separated step values (ms)")
	lengths := flag.String("lengths", "5000", "comma-separated length values (ms)")
	keeps := flag.String("keeps", "0", "comma-separated keep values (ms)")
	acs := flag.String("acs", "1280", "comma-separated ac values (0=whisper default)")
	cpuOnly := flag.Bool("cpu", false, "CPU only mode")
	settle := flag.Float64("settle", 1.5, "extra seconds to wait after playback ends")
	flag.Parse()

	if *modelPath == "" {
		log.Fatal("--model is required")
	}

	pairs, err := loadCorpus(*corpusDir)
	if err != nil {
		log.Fatalf("corpus: %v", err)
	}
	if len(pairs) == 0 {
		log.Fatalf("no .wav + .txt pairs found in %s", *corpusDir)
	}

	// Set up virtual PipeWire source.
	vs, err := createVirtualSource()
	if err != nil {
		log.Fatalf("virtual source: %v", err)
	}
	defer vs.cleanup()
	fmt.Fprintf(os.Stderr, "bench: virtual source created (sink=%q, source=%q, node=%d)\n", vs.sinkName, vs.sourceName, vs.sourceNodeID)

	stepVals := parseIntList(*steps)
	lengthVals := parseIntList(*lengths)
	keepVals := parseIntList(*keeps)
	acVals := parseIntList(*acs)

	var results []result
	total := len(stepVals) * len(lengthVals) * len(keepVals) * len(acVals) * len(pairs)
	run := 0

	for _, sv := range stepVals {
		for _, lv := range lengthVals {
			for _, kv := range keepVals {
				for _, av := range acVals {
					cfg := benchConfig{
						Model:      *modelPath,
						Lang:       *lang,
						Step:       sv,
						Length:     lv,
						Keep:       kv,
						AC:         av,
						CPUOnly:    *cpuOnly,
						DictateBin: *dictateBin,
					}
					for _, p := range pairs {
						run++
						fmt.Fprintf(os.Stderr, "\nbench: run %d/%d: %s step=%d len=%d keep=%d ac=%d\n",
							run, total, p.Name, sv, lv, kv, av)

						r, err := runBench(cfg, p, vs, *settle)
						if err != nil {
							fmt.Fprintf(os.Stderr, "bench: ERROR: %v\n", err)
							continue
						}
						results = append(results, r)
						fmt.Fprintf(os.Stderr, "bench: WER=%.1f%% hypothesis=%q\n", r.WER*100, r.Hypothesis)
					}
				}
			}
		}
	}

	printResults(results)
}

// --- Virtual PipeWire Source ---

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
// then connects the source node's output ports to them. Uses pw-link.
func rewireCapture(captureNode, sourceNode string) error {
	// List current links to find what's connected to the capture node.
	out, err := exec.Command("pw-link", "-lI").Output()
	if err != nil {
		return fmt.Errorf("pw-link -lI: %w", err)
	}

	// Parse pw-link output. Format:
	//   output_port_name
	//    -> input_port_name
	var currentOutput string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "   ") && strings.Contains(line, "-> ") {
			inputPort := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-> "))
			if strings.HasPrefix(inputPort, captureNode+":") {
				// Disconnect this link.
				fmt.Fprintf(os.Stderr, "bench: disconnect %s -> %s\n", currentOutput, inputPort)
				_ = exec.Command("pw-link", "-d", currentOutput, inputPort).Run()
			}
		} else if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, " ") {
			currentOutput = strings.TrimSpace(line)
		}
	}

	// Find source output ports and capture input ports.
	outPorts, err := listPorts("pw-link", "-o", sourceNode)
	if err != nil {
		return err
	}
	inPorts, err := listPorts("pw-link", "-i", captureNode)
	if err != nil {
		return err
	}

	// Connect matching ports (pair by index).
	n := len(outPorts)
	if len(inPorts) < n {
		n = len(inPorts)
	}
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
	for _, line := range strings.Split(string(out), "\n") {
		port := strings.TrimSpace(line)
		if strings.HasPrefix(port, nodePrefix+":") {
			ports = append(ports, port)
		}
	}
	return ports, nil
}

// --- Benchmark Execution ---

func runBench(cfg benchConfig, c corpus, vs *virtualSource, settleSeconds float64) (result, error) {
	start := time.Now()

	// Build dictate args.
	args := []string{
		"--model", cfg.Model,
		"--lang", cfg.Lang,
		"--pw-node", strconv.Itoa(vs.sourceNodeID),
		"--step", strconv.Itoa(cfg.Step),
		"--length", strconv.Itoa(cfg.Length),
		"--keep", strconv.Itoa(cfg.Keep),
	}
	if cfg.AC > 0 {
		args = append(args, "--ac", strconv.Itoa(cfg.AC))
	}
	if cfg.CPUOnly {
		args = append(args, "--cpu")
	}

	cmd := exec.Command(cfg.DictateBin, args...)
	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return result{}, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return result{}, fmt.Errorf("start dictate: %w", err)
	}

	// Collect stdout lines in background.
	var lines []string
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
	}()

	// Let whisper-stream initialize (SDL2 + Vulkan/CPU setup).
	time.Sleep(3 * time.Second)

	// PIPEWIRE_NODE hint is unreliable for virtual loopback sources.
	// Force-rewire whisper-stream's inputs to our virtual source.
	if err := rewireCapture("whisper-stream", vs.sourceName); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return result{}, fmt.Errorf("rewire: %w", err)
	}

	// Play WAV into the virtual sink.
	fmt.Fprintf(os.Stderr, "bench: playing %s\n", c.WAVPath)
	playCmd := exec.Command("pw-cat", "--playback", "--target", vs.sinkName, c.WAVPath)
	playCmd.Stderr = os.Stderr
	if err := playCmd.Run(); err != nil {
		_ = cmd.Process.Kill()
		return result{}, fmt.Errorf("pw-cat playback: %w", err)
	}

	// Wait for whisper to process the final audio window.
	settle := time.Duration(float64(cfg.Step+cfg.Length)*float64(time.Millisecond)) +
		time.Duration(settleSeconds*float64(time.Second))
	fmt.Fprintf(os.Stderr, "bench: settling %.1fs for final inference\n", settle.Seconds())
	time.Sleep(settle)

	// Stop dictate.
	forcedStop := stopProcess(cmd, 5*time.Second)
	<-scanDone

	elapsed := time.Since(start)

	// Strip timestamps and join all transcribed text.
	hypothesis := extractText(lines)
	wer := computeWER(c.Reference, hypothesis)
	encodeMS := extractEncodeMS(stderrBuf.String())
	headroomMS := 0.0
	if encodeMS > 0 {
		headroomMS = float64(cfg.Step) - encodeMS
	}

	return result{
		Config:     cfg,
		Corpus:     c.Name,
		Hypothesis: hypothesis,
		Reference:  c.Reference,
		WER:        wer,
		EncodeMS:   encodeMS,
		HeadroomMS: headroomMS,
		ForcedStop: forcedStop,
		Duration:   elapsed,
	}, nil
}

func stopProcess(cmd *exec.Cmd, timeout time.Duration) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		return false
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return true
	}
}

func extractEncodeMS(stderr string) float64 {
	m := encodeTimingRe.FindStringSubmatch(stderr)
	if len(m) != 3 {
		return 0
	}
	ms, err := strconv.ParseFloat(m[2], 64)
	if err != nil {
		return 0
	}
	return ms
}

// --- Output Parsing ---

func extractText(lines []string) string {
	var merged []string
	for _, line := range lines {
		text := timestampRe.ReplaceAllString(line, "")
		text = strings.TrimSpace(text)
		if text != "" {
			words := strings.Fields(text)
			if len(words) == 0 {
				continue
			}
			if len(merged) == 0 {
				merged = append(merged, words...)
				continue
			}
			overlap := longestTokenOverlap(merged, words)
			merged = append(merged, words[overlap:]...)
		}
	}
	return strings.Join(merged, " ")
}

func longestTokenOverlap(a, b []string) int {
	max := len(a)
	if len(b) < max {
		max = len(b)
	}
	for k := max; k >= 1; k-- {
		ok := true
		for i := 0; i < k; i++ {
			if normalizeToken(a[len(a)-k+i]) != normalizeToken(b[i]) {
				ok = false
				break
			}
		}
		if ok {
			return k
		}
	}
	return 0
}

func normalizeToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// --- WER (Word Error Rate) ---

func computeWER(reference, hypothesis string) float64 {
	ref := normalizeWords(reference)
	hyp := normalizeWords(hypothesis)

	if len(ref) == 0 {
		if len(hyp) == 0 {
			return 0
		}
		return 1
	}

	// Levenshtein distance on word sequences.
	n, m := len(ref), len(hyp)
	prev := make([]int, m+1)
	curr := make([]int, m+1)

	for j := 0; j <= m; j++ {
		prev[j] = j
	}

	for i := 1; i <= n; i++ {
		curr[0] = i
		for j := 1; j <= m; j++ {
			cost := 1
			if ref[i-1] == hyp[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}

	return float64(prev[m]) / float64(n)
}

func normalizeWords(s string) []string {
	s = strings.ToLower(s)
	var buf strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
			buf.WriteRune(r)
		} else {
			buf.WriteRune(' ')
		}
	}
	return strings.Fields(buf.String())
}

func min(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// --- Corpus Loading ---

func loadCorpus(dir string) ([]corpus, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var pairs []corpus
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".wav") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".wav")
		txtPath := filepath.Join(dir, base+".txt")
		refBytes, err := os.ReadFile(txtPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench: skipping %s (no matching .txt)\n", e.Name())
			continue
		}
		pairs = append(pairs, corpus{
			Name:      base,
			WAVPath:   filepath.Join(dir, e.Name()),
			Reference: strings.TrimSpace(string(refBytes)),
		})
	}
	return pairs, nil
}

// --- Results Output ---

func printResults(results []result) {
	if len(results) == 0 {
		fmt.Println("no results")
		return
	}

	fmt.Println()
	fmt.Println("=== Benchmark Results ===")
	fmt.Println()
	fmt.Printf("%-12s %5s %6s %4s %3s   %6s  %7s  %8s  %5s  %8s  %s\n",
		"corpus", "step", "length", "keep", "ac", "WER", "enc-ms", "headroom", "stop", "time", "hypothesis")
	fmt.Println(strings.Repeat("-", 100))

	for _, r := range results {
		stop := "term"
		if r.ForcedStop {
			stop = "kill"
		}
		fmt.Printf("%-12s %5d %6d %4d %3d   %5.1f%%  %7.1f  %8.1f  %5s  %7.1fs  %.60s\n",
			r.Corpus,
			r.Config.Step,
			r.Config.Length,
			r.Config.Keep,
			r.Config.AC,
			r.WER*100,
			r.EncodeMS,
			r.HeadroomMS,
			stop,
			r.Duration.Seconds(),
			r.Hypothesis,
		)
	}
}

// --- Helpers ---

func parseIntList(s string) []int {
	parts := strings.Split(s, ",")
	vals := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			log.Fatalf("invalid integer %q", p)
		}
		vals = append(vals, v)
	}
	return vals
}
