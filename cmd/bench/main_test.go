package main

import "testing"

func TestComputeWER(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		hyp  string
		want float64
	}{
		{name: "exact", ref: "hello world", hyp: "hello world", want: 0},
		{name: "insertion", ref: "hello world", hyp: "hello there world", want: 0.5},
		{name: "normalization", ref: "Hello, World!", hyp: "hello world", want: 0},
		{name: "empty-ref", ref: "", hyp: "anything", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeWER(tt.ref, tt.hyp)
			if got != tt.want {
				t.Fatalf("computeWER() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractTextMergesOverlap(t *testing.T) {
	lines := []string{
		"[00:07.8 Δ3.0s] Okay, so I want you to help me",
		"[00:10.8 Δ3.0s] help me on the design and",
		"[00:13.9 Δ3.0s] and breakdown of the migration",
	}

	got := extractText(lines)
	want := "Okay, so I want you to help me on the design and breakdown of the migration"
	if got != want {
		t.Fatalf("extractText() = %q, want %q", got, want)
	}
}

func TestExtractEncodeMS(t *testing.T) {
	stderr := "whisper_print_timings:   encode time = 48419.30 ms /    30 runs (  1613.98 ms per run)"
	got := extractEncodeMS(stderr)
	if got != 1613.98 {
		t.Fatalf("extractEncodeMS() = %v, want 1613.98", got)
	}
}
