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
