//go:build integration

package integ

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

var rowRe = regexp.MustCompile(`(?m)^design_en\s+(?P<step>\d+)\s+(?P<length>\d+)\s+(?P<keep>\d+)\s+(?P<ac>\d+)\s+(?P<wer>[\d.]+)%\s+(?P<enc>[\d.]+)\s+(?P<headroom>-?[\d.]+)\s+(?P<stop>\w+)\s+(?P<time>[\d.]+)s`)

type runResult struct {
	wer        float64
	headroomMS float64
	stop       string
	output     string
}

func TestDefaultBenchProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	repeats := envInt("DICTATE_INTEG_REPEATS", 3)
	maxMedianWER := envFloat("DICTATE_INTEG_MAX_MEDIAN_WER", 18.0)
	minMedianHeadroom := envFloat("DICTATE_INTEG_MIN_MEDIAN_HEADROOM_MS", 500.0)
	root := repoRoot(t)

	require.FileExists(t, filepath.Join(root, "bin/bench"))
	require.FileExists(t, filepath.Join(root, "bin/dictate"))
	require.FileExists(t, filepath.Join(root, "bin/whisper-stream"))
	require.FileExists(t, filepath.Join(root, "models/ggml-large-v3-turbo-q5_0.bin"))
	require.FileExists(t, filepath.Join(root, "bench/corpus/design_en.wav"))
	require.FileExists(t, filepath.Join(root, "bench/corpus/design_en.txt"))

	results := make([]runResult, 0, repeats)
	for i := 0; i < repeats; i++ {
		cmd := exec.Command(
			filepath.Join(root, "bin/bench"),
			"--model", filepath.Join(root, "models/ggml-large-v3-turbo-q5_0.bin"),
			"--corpus", filepath.Join(root, "bench/corpus"),
			"--dictate", filepath.Join(root, "bin/dictate"),
			"--lang", "en",
		)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "bench run %d failed:\n%s", i+1, string(out))

		res, ok := parseRunResult(string(out))
		require.True(t, ok, "failed to parse bench output for run %d:\n%s", i+1, string(out))
		results = append(results, res)
		t.Logf("run=%d wer=%.1f%% headroom=%.1fms stop=%s", i+1, res.wer, res.headroomMS, res.stop)
		require.Equal(t, "term", res.stop, "run %d did not stop cleanly:\n%s", i+1, res.output)
	}

	medianWER := medianWER(results)
	medianHeadroom := medianHeadroom(results)
	t.Logf("median wer=%.1f%% median headroom=%.1fms", medianWER, medianHeadroom)
	require.LessOrEqual(t, medianWER, maxMedianWER, "median WER too high")
	require.GreaterOrEqual(t, medianHeadroom, minMedianHeadroom, "median headroom too low")
}

func parseRunResult(out string) (runResult, bool) {
	m := rowRe.FindStringSubmatch(out)
	if m == nil {
		return runResult{}, false
	}
	idx := func(name string) int { return rowRe.SubexpIndex(name) }
	wer, err1 := strconv.ParseFloat(m[idx("wer")], 64)
	headroom, err2 := strconv.ParseFloat(m[idx("headroom")], 64)
	if err1 != nil || err2 != nil {
		return runResult{}, false
	}
	return runResult{
		wer:        wer,
		headroomMS: headroom,
		stop:       m[idx("stop")],
		output:     out,
	}, true
}

func medianWER(results []runResult) float64 {
	vals := make([]float64, 0, len(results))
	for _, r := range results {
		vals = append(vals, r.wer)
	}
	return median(vals)
}

func medianHeadroom(results []runResult) float64 {
	vals := make([]float64, 0, len(results))
	for _, r := range results {
		vals = append(vals, r.headroomMS)
	}
	return median(vals)
}

func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func envInt(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			return v
		}
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if raw := os.Getenv(key); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v
		}
	}
	return fallback
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "resolve test file path")
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}
