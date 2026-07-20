package tools

import "testing"

func TestIsPlainSessionID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"550e8400-e29b-41d4-a716-446655440000", true},
		{"background:550e8400-e29b-41d4-a716-446655440000", false},
		{"delegate:550e8400-e29b-41d4-a716-446655440000", false},
		{"peer:some-slug", false},
		{"", false},
		{"not-a-uuid", false},
	}
	for _, c := range cases {
		if got := isPlainSessionID(c.id); got != c.want {
			t.Errorf("isPlainSessionID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}
