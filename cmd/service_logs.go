package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/mbarney/gcalsync/internal/service"
	"github.com/spf13/cobra"
)

var serviceLogsLines int

var serviceLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "View service logs",
	RunE:  runServiceLogs,
}

func init() {
	serviceLogsCmd.Flags().IntVar(&serviceLogsLines, "lines", 0, "number of lines to show (default: follow)")
	serviceCmd.AddCommand(serviceLogsCmd)
}

func runServiceLogs(cmd *cobra.Command, args []string) error {
	svc, err := service.NewLaunchdService(0)
	if err != nil {
		return err
	}

	logPath := svc.LogPath()
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		fmt.Println("No log file found — service may not have run yet")
		return nil
	}

	var tailCmd *exec.Cmd
	if serviceLogsLines > 0 {
		tailCmd = exec.Command("tail", "-n", strconv.Itoa(serviceLogsLines), logPath)
	} else {
		tailCmd = exec.Command("tail", "-f", logPath)
	}
	tailCmd.Stdout = os.Stdout
	tailCmd.Stderr = os.Stderr
	return tailCmd.Run()
}
