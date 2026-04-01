package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var accountListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured accounts",
	RunE:  runAccountList,
}

func init() {
	accountCmd.AddCommand(accountListCmd)
}

func runAccountList(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if len(cfg.Accounts) == 0 {
		fmt.Println("No accounts configured")
		return nil
	}

	for name, acct := range cfg.Accounts {
		enterprise := ""
		if acct.Enterprise {
			enterprise = " (enterprise)"
		}
		fmt.Printf("  %s%s — %d calendar(s)\n", name, enterprise, len(acct.Calendars))
	}
	return nil
}
