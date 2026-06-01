package main

import (
	"context"

	"github.com/dopesoft/infinity/core/internal/push"
	"github.com/dopesoft/infinity/core/internal/server"
)

// watchNotifier adapts the watch_until poller's delivery to Infinity's two
// reach channels: the live chat (a proactive_message scoped to the originating
// session) and a push notification (so the boss learns whether or not a tab is
// open). This mirrors how background_build's OnDone is delivered, and is the
// whole point of the watch substrate - keeping the "I'll report back" promise
// even after the turn that made it has ended.
type watchNotifier struct {
	srv  *server.Server
	push *push.Sender
}

func (w *watchNotifier) Notify(sessionID, text string) {
	if w == nil {
		return
	}
	if w.srv != nil {
		w.srv.NotifySession(sessionID, text)
	}
	if w.push != nil {
		w.push.Notify(context.Background(), push.Notification{
			Title:    "Jarvis",
			Body:     clipPush(text, 160),
			Kind:     "watch",
			Tag:      "watch",
			URL:      "/live",
			Renotify: true,
		})
	}
}
