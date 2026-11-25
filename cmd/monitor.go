package cmd

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/hasoftware/claude-credentials-manager/v2/internal/serve"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{Use: "monitor", Short: "Monitor the Linux gateway host and publish status for the dashboard", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return serve.RunSystemMonitor(ctx)
	}})
}
