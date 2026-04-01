package cmd

import (
	"fmt"

	"github.com/mbarney/gcalsync/internal/service"
	"github.com/spf13/cobra"
)

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the gcalsync launchd service",
	RunE:  runServiceUninstall,
}

func init() {
	serviceCmd.AddCommand(serviceUninstallCmd)
}

func runServiceUninstall(cmd *cobra.Command, args []string) error {
	svc, err := service.NewLaunchdService(0)
	if err != nil {
		return err
	}

	if err := svc.Uninstall(); err != nil {
		return err
	}

	fmt.Println("Service uninstalled")
	return nil
}
