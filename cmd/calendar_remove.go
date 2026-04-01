package cmd

import (
	"fmt"

	"github.com/mbarney/gcalsync/internal/config"
	"github.com/spf13/cobra"
)

var calendarRemoveCmd = &cobra.Command{
	Use:   "remove <account> <calendar-id>",
	Short: "Remove a calendar from an account",
	Args:  cobra.ExactArgs(2),
	RunE:  runCalendarRemove,
}

func init() {
	calendarCmd.AddCommand(calendarRemoveCmd)
}

func runCalendarRemove(cmd *cobra.Command, args []string) error {
	accountName, calID := args[0], args[1]

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	acct, exists := cfg.Accounts[accountName]
	if !exists {
		return fmt.Errorf("account %q not found", accountName)
	}

	idx := -1
	for i, c := range acct.Calendars {
		if c == calID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("calendar %q not found in account %q", calID, accountName)
	}

	acct.Calendars = append(acct.Calendars[:idx], acct.Calendars[idx+1:]...)
	cfg.Accounts[accountName] = acct
	if err := config.Save(getConfigDir(), cfg); err != nil {
		return err
	}

	fmt.Printf("Calendar %q removed from account %q\n", calID, accountName)
	fmt.Println("Tip: run `gcalsync desync` first to remove existing blockers")
	return nil
}
