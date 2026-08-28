package main

import (
	"fmt"
	"log/slog"
	"os"

	// The runtime image is gcr.io/distroless/static-debian12, which ships no
	// /usr/share/zoneinfo. Without this every time.LoadLocation in the binary
	// fails and silently falls back to UTC, so anything reasoning about a local
	// day boundary (cron schedules, quota windows, the pursuit day counter)
	// rolls over at 7pm Chicago instead of midnight - and disagrees with
	// Postgres, which carries its own tzdata and gets it right.
	_ "time/tzdata"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Honest severity on Railway. The log shipper tags lines by stream
	// (stdout -> info, stderr -> error); slog's default text handler writes
	// to stderr, so every Info/Warn line shows up as a fake red error.
	// Emit JSON to stdout instead so Railway reads the explicit "level"
	// field and surfaces each line in its true state (info / warn / error).
	// See CLAUDE.md "Logging — severity must match reality."
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	root := &cobra.Command{
		Use:           "infinity",
		Short:         "Infinity - single-user AI agent with persistent memory",
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(serveCmd())
	root.AddCommand(migrateCmd())
	root.AddCommand(doctorCmd())
	root.AddCommand(consolidateCmd())
	root.AddCommand(newBackfillCircleCmd())
	root.AddCommand(reflectCmd())
	root.AddCommand(gymCmd())
	root.AddCommand(vapidCmd())
	root.AddCommand(backfillArtifactsCmd())
	root.AddCommand(backfillBodiesCmd())
	root.AddCommand(backfillTitlesCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
