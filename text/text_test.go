package text

import "testing"

func TestNormalizeToken(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Hello!", "hello"},
		{"it's", "it's"},
		{"café", "café"},
		{"123abc", "123abc"},
		{"--dash--", "dash"},
	}
	for _, tt := range tests {
		if got := NormalizeToken(tt.in); got != tt.want {
			t.Errorf("NormalizeToken(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOverlapCount(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want int
	}{
		{"suffix-prefix", []string{"help", "me", "on", "the", "design", "and"}, []string{"and", "breakdown", "of"}, 1},
		{"full-overlap", []string{"thank", "you"}, []string{"thank", "you"}, 2},
		{"no-overlap", []string{"one", "sentence"}, []string{"different", "start"}, 0},
		{"empty-a", nil, []string{"hello"}, 0},
		{"empty-b", []string{"hello"}, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OverlapCount(tt.a, tt.b); got != tt.want {
				t.Errorf("OverlapCount() = %d, want %d", got, tt.want)
			}
		})
	}
}
