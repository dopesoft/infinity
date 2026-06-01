package memory

import "testing"

func TestNormalizeLessonsJSONReturnsNilForScalar(t *testing.T) {
	got := normalizeLessonsJSON(`"oops"`)
	if len(got) != 0 {
		t.Fatalf("expected empty lessons for scalar JSON, got %#v", got)
	}
}

func TestNormalizeLessonsJSONParsesArray(t *testing.T) {
	got := normalizeLessonsJSON(`[{"text":"do the thing","confidence":0.9}]`)
	if len(got) != 1 {
		t.Fatalf("expected 1 lesson, got %#v", got)
	}
	if got[0].Text != "do the thing" || got[0].Confidence != 0.9 {
		t.Fatalf("unexpected lesson decode: %#v", got[0])
	}
}
