package cmd

import (
	"fmt"

	"github.com/mbarney/gcalsync/internal/service"
	"github.com/spf13/cobra"
)

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show service status",
	RunE:  runServiceStatus,
}

func init() {
	serviceCmd.AddCommand(serviceStatusCmd)
}

func runServiceStatus(cmd *cobra.Command, args []string) error {
	svc, err := service.NewLaunchdService(0)
	if err != nil {
		return err
	}

	status, err := svc.Status()
	if err != nil {
		return err
	}

	if !status.Installed {
		fmt.Println("Service: not installed")
		return nil
	}

	fmt.Println("Service: installed")
	if status.Running {
		fmt.Printf("Status: running (PID %d)\n", status.PID)
	} else {
		fmt.Println("Status: not running")
	}
	fmt.Printf("Last exit code: %d\n", status.LastExit)
	fmt.Printf("Logs: %s\n", svc.LogPath())
	return nil
}
