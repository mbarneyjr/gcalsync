package cmd

import (
	"fmt"
	"time"

	"github.com/mbarney/gcalsync/internal/service"
	"github.com/spf13/cobra"
)

var serviceInterval string

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install gcalsync as a launchd service",
	RunE:  runServiceInstall,
}

func init() {
	serviceInstallCmd.Flags().StringVar(&serviceInterval, "interval", "15m", "sync interval")
	serviceCmd.AddCommand(serviceInstallCmd)
}

func runServiceInstall(cmd *cobra.Command, args []string) error {
	interval, err := time.ParseDuration(serviceInterval)
	if err != nil {
		return fmt.Errorf("invalid interval %q: %w", serviceInterval, err)
	}

	svc, err := service.NewLaunchdService(interval)
	if err != nil {
		return err
	}

	if err := svc.Install(); err != nil {
		return err
	}

	fmt.Printf("Service installed (interval: %s)\n", interval)
	fmt.Printf("Logs: %s\n", svc.LogPath())
	return nil
}
