package output

import "testing"

func TestFormatTypedChunk(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "keep raw text",
			in:   "hello world",
			want: "hello world ",
		},
		{
			name: "trim whitespace",
			in:   "  hello world  ",
			want: "hello world ",
		},
		{
			name: "empty after cleanup",
			in:   "   ",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTypedChunk(tt.in); got != tt.want {
				t.Fatalf("formatTypedChunk() = %q, want %q", got, tt.want)
			}
		})
	}
}
