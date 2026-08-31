package memory

import "testing"

// The live profile row is the first case here and the reason this function
// exists in its current shape: a lookup that only understood a bare "Kai"
// would have greeted the boss namelessly forever.
func TestNormalizeBossName(t *testing.T) {
	cases := map[string]string{
		"I am Kai.. real name Khaya.. but sometimes you call me boss.": "Kai",
		"Kai":                              "Kai",
		"  Kai  ":                          "Kai",
		"Kai, though legal docs say Khaya": "Kai",
		"My name is Kai":                   "Kai",
		"I'm Kai":                          "Kai",
		"Call me boss":                     "boss",
		"boss":                             "boss",
		"Kai Malabie":                      "Kai Malabie",
		"Kai Malabie the third":            "Kai",
		"\"Kai\"":                          "Kai",
		"Kai. He runs Dopesoft.":           "Kai",
		"Reginald Archibald Bartholomew Fitzwilliam": "Reginald",
		// Nothing usable must yield nothing, never a fragment.
		"":     "",
		"   ":  "",
		".":    "",
		"I am": "",
		"K":    "",
	}
	for in, want := range cases {
		if got := normalizeBossName(in); got != want {
			t.Errorf("normalizeBossName(%q) = %q, want %q", in, got, want)
		}
	}
}
