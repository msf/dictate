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

	"dictate/internal/audio"
	"dictate/internal/output"
	"dictate/internal/whisper"
)

func main() {
	model := flag.String("model", "", "path to whisper model (default: largest ggml-*.bin in models/)")
	lang := flag.String("lang", "auto", "language code (auto, en, pt, etc.)")
	device := flag.String("device", "", "audio source: PipeWire node ID or substring match on name/description")
	outFile := flag.String("file", "", "also write output to this file (append mode)")
	cpuOnly := flag.Bool("cpu", false, "disable GPU inference, use CPU only")
	listDevices := flag.Bool("list-devices", false, "list audio sources and exit")
	pwNode := flag.Int("pw-node", 0, "PipeWire node ID (bypasses mic detection)")
	step := flag.Int("step", 3000, "inference step interval in ms")
	length := flag.Int("length", 8000, "audio window length in ms")
	keep := flag.Int("keep", 200, "audio context kept between windows in ms")
	ac := flag.Int("ac", 0, "audio context limit (0 = whisper default)")
	flag.Parse()

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
	fmt.Fprintf(os.Stderr, "dictate: step=%dms length=%dms keep=%dms", *step, *length, *keep)
	if *ac > 0 {
		fmt.Fprintf(os.Stderr, " ac=%d", *ac)
	}
	fmt.Fprintf(os.Stderr, "\n")

	// Always write to stdout. Optionally tee to a file.
	var sink output.Sink = output.StdoutSink{}
	if *outFile != "" {
		fsink, err := output.NewFileSink(*outFile)
		if err != nil {
			log.Fatalf("open output file: %v", err)
		}
		defer fsink.Close()
		sink = output.NewMultiSink(output.StdoutSink{}, fsink)
	}

	proc := whisper.NewProcess(whisper.Config{
		StreamBin: streamBin,
		Model:     *model,
		Lang:      *lang,
		Threads:   threads,
		PwNodeID:  micID,
		CPUOnly:   *cpuOnly,
		OnText:    sink.Write,
		Step:      *step,
		Length:    *length,
		Keep:      *keep,
		AC:        *ac,
	})

	if err := proc.Start(); err != nil {
		log.Fatalf("start whisper: %v", err)
	}

	fmt.Fprintf(os.Stderr, "dictate: listening (SIGUSR1=toggle, SIGTERM=stop)\n")

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1)

	for sig := range sigs {
		switch sig {
		case syscall.SIGUSR1:
			proc.Toggle()
		default:
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

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
