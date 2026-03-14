package whisper

import "testing"

func TestTrimLeadingOverlap(t *testing.T) {
	tests := []struct {
		name string
		prev string
		curr string
		want string
	}{
		{
			name: "word-overlap",
			prev: "help me on the design and",
			curr: "and breakdown of the migration",
			want: "breakdown of the migration",
		},
		{
			name: "full-overlap",
			prev: "thank you",
			curr: "thank you",
			want: "",
		},
		{
			name: "no-overlap",
			prev: "one sentence",
			curr: "different start",
			want: "different start",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimLeadingOverlap(tt.prev, tt.curr)
			if got != tt.want {
				t.Fatalf("trimLeadingOverlap() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStreamArgsPreserveZeroKeep(t *testing.T) {
	args := streamArgs(Config{
		Model:   "model.bin",
		Lang:    "en",
		Threads: 8,
		Step:    3000,
		Length:  6000,
		Keep:    0,
		AC:      0,
	})

	want := []string{"-m", "model.bin", "-l", "en", "-t", "8", "--step", "3000", "--length", "6000", "--keep", "0"}
	if len(args) != len(want) {
		t.Fatalf("streamArgs() len = %d, want %d: %v", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("streamArgs()[%d] = %q, want %q (all args: %v)", i, args[i], want[i], args)
		}
	}
}
