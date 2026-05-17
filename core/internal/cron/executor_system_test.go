package cron

import "testing"

func TestJobKindSystemTaskValid(t *testing.T) {
	if !JobSystemTask.Valid() {
		t.Fatal("system_task should be a valid cron job kind")
	}
}
