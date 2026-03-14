package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"dictate/audio"
	"dictate/output"
	"dictate/whisper"
)

func main() {
	model := flag.String("model", "", "path to whisper model (default: largest ggml-*.bin in models/)")
	lang := flag.String("lang", "auto", "language code (auto, en, pt, etc.)")
	device := flag.String("device", "", "audio source: PipeWire node ID or substring match on name/description")
	outputMode := flag.String("output", "stdout", "output mode: stdout or type")
	outFile := flag.String("file", "", "also write output to this file (append mode)")
	rawFile := flag.String("raw-file", "", "also write raw text chunks to this file (append mode, no timestamps)")
	cpuOnly := flag.Bool("cpu", false, "disable GPU inference, use CPU only")
	listDevices := flag.Bool("list-devices", false, "list audio sources and exit")
	pwNode := flag.Int("pw-node", 0, "PipeWire node ID (bypasses mic detection)")
	step := flag.Int("step", 2500, "inference step interval in ms")
	length := flag.Int("length", 5000, "audio window length in ms")
	keep := flag.Int("keep", 0, "audio context kept between windows in ms")
	ac := flag.Int("ac", 1280, "audio context limit (0 = whisper default)")
	idleTimeout := flag.Duration("silence-timeout", 0, "stop after this much transcription silence (for example 15s); 0 disables")
	flag.Parse()

	if *outputMode != "stdout" && *outputMode != "type" {
		log.Fatalf("unsupported --output %q (want stdout or type)", *outputMode)
	}

	if *listDevices {
		sources, err := audio.ListSources()
		if err != nil {
			log.Fatalf("mic detection: %v", err)
		}
		for _, s := range sources {
			fmt.Fprintf(os.Stdout, "[%d] %s\n     %s\n", s.ID, s.Description, s.Name)
		}
		return
	}

	if *model == "" {
		*model = detectModel()
	}

	threads := runtime.NumCPU()

	streamBin, err := findStreamBinary()
	if err != nil {
		log.Fatal(err)
	}

	var micID int
	if *pwNode > 0 {
		micID = *pwNode
		fmt.Fprintf(os.Stderr, "dictate: pw-node %d (direct)\n", micID)
	} else {
		sources, err := audio.ListSources()
		if err != nil {
			log.Fatalf("mic detection: %v", err)
		}
		mic, err := audio.FindSource(sources, *device)
		if err != nil {
			log.Fatal(err)
		}
		micID = mic.ID
		fmt.Fprintf(os.Stderr, "dictate: mic [%d] %s\n", mic.ID, mic.Description)
	}

	inference := "gpu"
	if *cpuOnly {
		inference = "cpu"
	}
	fmt.Fprintf(os.Stderr, "dictate: model %s\n", filepath.Base(*model))
	fmt.Fprintf(os.Stderr, "dictate: lang=%s threads=%d inference=%s\n", *lang, threads, inference)
	fmt.Fprintf(os.Stderr, "dictate: output=%s\n", *outputMode)
	fmt.Fprintf(os.Stderr, "dictate: step=%dms length=%dms keep=%dms", *step, *length, *keep)
	if *ac > 0 {
		fmt.Fprintf(os.Stderr, " ac=%d", *ac)
	}
	fmt.Fprintf(os.Stderr, "\n")

	var sinks []output.Sink
	switch *outputMode {
	case "stdout":
		sinks = append(sinks, output.StdoutSink{})
	case "type":
		tsink, err := output.NewTypeSink()
		if err != nil {
			log.Fatalf("setup typed output: %v", err)
		}
		sinks = append(sinks, tsink)
	}

	if *outFile != "" {
		fsink, err := output.NewFileSink(*outFile)
		if err != nil {
			log.Fatalf("open output file: %v", err)
		}
		sinks = append(sinks, fsink)
	}
	if *rawFile != "" {
		rsink, err := output.NewRawFileSink(*rawFile)
		if err != nil {
			log.Fatalf("open raw output file: %v", err)
		}
		sinks = append(sinks, rsink)
	}

	sink := output.NewMultiSink(sinks...)
	defer sink.Close()

	// Activity fires on any whisper output (before hallucination/overlap
	// filtering) so the idle timer stays alive while the mic is active.
	activity := make(chan struct{}, 1)
	onActivity := func() {
		select {
		case activity <- struct{}{}:
		default:
		}
	}

	onText := func(raw, display string) {
		sink.Write(raw, display)
	}

	proc := whisper.NewProcess(whisper.Config{
		StreamBin:  streamBin,
		Model:      *model,
		Lang:       *lang,
		Threads:    threads,
		PwNodeID:   micID,
		CPUOnly:    *cpuOnly,
		OnText:     onText,
		OnActivity: onActivity,
		Step:       *step,
		Length:     *length,
		Keep:       *keep,
		AC:         *ac,
	})

	if err := proc.Start(); err != nil {
		log.Fatalf("start whisper: %v", err)
	}

	done := make(chan struct{})
	fmt.Fprintf(os.Stderr, "dictate: listening (SIGUSR1=toggle, SIGTERM=stop)\n")
	if *idleTimeout > 0 {
		fmt.Fprintf(os.Stderr, "dictate: silence-timeout=%s\n", idleTimeout.String())
		go func(timeout time.Duration) {
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			for {
				select {
				case <-activity:
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(timeout)
				case <-timer.C:
					fmt.Fprintf(os.Stderr, "dictate: silence timeout reached, stopping\n")
					close(done)
					return
				}
			}
		}(*idleTimeout)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)

	for {
		select {
		case sig := <-sigs:
			switch sig {
			case syscall.SIGUSR1:
				proc.Toggle()
			default:
				fmt.Fprintf(os.Stderr, "\ndictate: shutting down\n")
				proc.Stop()
				return
			}
		case <-done:
			fmt.Fprintf(os.Stderr, "\ndictate: shutting down\n")
			proc.Stop()
			return
		}
	}
}

// detectModel finds the largest ggml-*.bin file in the models/ directory.
// Prefers bigger models (turbo 548M > small 466M > base 142M > tiny 75M).
func detectModel() string {
	modelsDir := modelsDirectory()

	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		log.Fatalf("no models found in %s: %v", modelsDir, err)
	}

	type candidate struct {
		path string
		size int64
	}
	var candidates []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ggml-") || !strings.HasSuffix(e.Name(), ".bin") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{
			path: filepath.Join(modelsDir, e.Name()),
			size: info.Size(),
		})
	}

	if len(candidates) == 0 {
		log.Fatalf("no ggml-*.bin models found in %s (run 'make models')", modelsDir)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].size > candidates[j].size
	})

	return candidates[0].path
}

func modelsDirectory() string {
	exe, err := os.Executable()
	if err != nil {
		return "models"
	}
	return filepath.Join(filepath.Dir(exe), "..", "models")
}

func findStreamBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	candidate := filepath.Join(filepath.Dir(exe), "whisper-stream")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("whisper-stream not found at %s (run 'make whisper' first)", candidate)
}
