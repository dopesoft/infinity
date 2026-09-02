package server

import "testing"

// A reload must tell three things apart, and before this it could not:
// a command still running, a command whose turn died before it returned, and
// a command that finished and printed nothing. All three had "no output", and
// all three spun forever. The boss: "im left with spinners only."
func TestToolRowState_TellsRunningStoppedAndDoneApart(t *testing.T) {
	cases := []struct {
		name                   string
		running, turnLive      bool
		wantRun, wantInterrupt bool
	}{
		{"filed at start, turn still going", true, true, true, false},
		{"filed at start, turn ended without a result", true, false, false, true},
		{"finished (even with empty output), turn still going", false, true, false, false},
		{"finished (even with empty output), turn ended", false, false, false, false},
	}
	for _, c := range cases {
		gotRun, gotInterrupt := toolRowState(c.running, c.turnLive)
		if gotRun != c.wantRun || gotInterrupt != c.wantInterrupt {
			t.Errorf("%s: running=%v interrupted=%v, want %v/%v",
				c.name, gotRun, gotInterrupt, c.wantRun, c.wantInterrupt)
		}
	}
}
