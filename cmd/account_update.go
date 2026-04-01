package cmd

import (
	"fmt"

	"github.com/mbarney/gcalsync/internal/config"
	"github.com/spf13/cobra"
)

var accountUpdateEnterprise bool

var accountUpdateCmd = &cobra.Command{
	Use:   "update <name>",
	Short: "Update account settings",
	Args:  cobra.ExactArgs(1),
	RunE:  runAccountUpdate,
}

func init() {
	accountUpdateCmd.Flags().BoolVar(&accountUpdateEnterprise, "enterprise", true, "enterprise account (supports out-of-office events)")
	accountCmd.AddCommand(accountUpdateCmd)
}

func runAccountUpdate(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	acct, exists := cfg.Accounts[name]
	if !exists {
		return fmt.Errorf("account %q not found", name)
	}

	if cmd.Flags().Changed("enterprise") {
		acct.Enterprise = accountUpdateEnterprise
	}

	cfg.Accounts[name] = acct
	if err := config.Save(getConfigDir(), cfg); err != nil {
		return err
	}

	fmt.Printf("Account %q updated\n", name)
	return nil
}
