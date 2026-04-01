package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var calendarListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured calendars",
	RunE:  runCalendarList,
}

func init() {
	calendarCmd.AddCommand(calendarListCmd)
}

func runCalendarList(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if len(cfg.Accounts) == 0 {
		fmt.Println("No accounts configured")
		return nil
	}

	for name, acct := range cfg.Accounts {
		fmt.Printf("%s:\n", name)
		if len(acct.Calendars) == 0 {
			fmt.Println("  (no calendars)")
			continue
		}
		for _, cal := range acct.Calendars {
			fmt.Printf("  %s\n", cal)
		}
	}
	return nil
}
