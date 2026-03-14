package main

import (
	"bufio"
	"bytes"
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
	pwLinkPortRe   = regexp.MustCompile(`^\s*\d+\s+(\S+:\S+)$`)
	pwLinkEdgeRe   = regexp.MustCompile(`^\s*\d+\s+\|->\s+\d+\s+(\S+:\S+)$`)
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

// --- Benchmark Execution ---

func runBench(cfg benchConfig, c corpus, vs *virtualSource, settleSeconds float64) (result, error) {
	start := time.Now()

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

	fmt.Fprintf(os.Stderr, "bench: playing %s\n", c.WAVPath)
	playCmd := exec.Command("pw-cat", "--playback", "--target", vs.sinkName, c.WAVPath)
	playCmd.Stderr = os.Stderr
	if err := playCmd.Run(); err != nil {
		_ = cmd.Process.Kill()
		return result{}, fmt.Errorf("pw-cat playback: %w", err)
	}

	settleTime := time.Duration(float64(cfg.Step+cfg.Length)*float64(time.Millisecond)) +
		time.Duration(settleSeconds*float64(time.Second))
	fmt.Fprintf(os.Stderr, "bench: settling %.1fs for final inference\n", settleTime.Seconds())
	time.Sleep(settleTime)

	forcedStop := stopProcess(cmd, 5*time.Second)
	<-scanDone

	elapsed := time.Since(start)

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
