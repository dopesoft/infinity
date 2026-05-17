package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
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
	root.AddCommand(reflectCmd())
	root.AddCommand(gymCmd())
	root.AddCommand(vapidCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
