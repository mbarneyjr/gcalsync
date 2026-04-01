package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/mbarney/gcalsync/internal/auth"
	"github.com/mbarney/gcalsync/internal/config"
	"github.com/spf13/cobra"
)

var accountAddEnterprise bool

var accountAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a Google account",
	Args:  cobra.ExactArgs(1),
	RunE:  runAccountAdd,
}

func init() {
	accountAddCmd.Flags().BoolVar(&accountAddEnterprise, "enterprise", true, "enterprise account (supports out-of-office events)")
	accountCmd.AddCommand(accountAddCmd)
}

func runAccountAdd(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	if _, exists := cfg.Accounts[name]; exists {
		return fmt.Errorf("account %q already exists", name)
	}

	dir := getConfigDir()
	store := &auth.TokenStore{ConfigDir: dir}
	flow := &auth.OAuthFlow{
		ClientID:     cfg.Google.ClientID,
		ClientSecret: cfg.Google.ClientSecret,
		Ports:        cfg.General.AuthorizedPorts,
		TokenStore:   store,
	}

	fmt.Printf("Authenticating account %q...\n", name)
	if _, err := flow.Authenticate(name); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	cfg.Accounts[name] = config.AccountConfig{
		Enterprise: accountAddEnterprise,
		Calendars:  []string{},
	}

	if err := config.Save(dir, cfg); err != nil {
		return err
	}

	fmt.Printf("Account %q added\n", name)

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Add a calendar now? (calendar ID, or empty to skip): ")
	calID, _ := reader.ReadString('\n')
	calID = strings.TrimSpace(calID)
	if calID != "" {
		acct := cfg.Accounts[name]
		acct.Calendars = append(acct.Calendars, calID)
		cfg.Accounts[name] = acct
		if err := config.Save(dir, cfg); err != nil {
			return err
		}
		fmt.Printf("Calendar %q added to account %q\n", calID, name)
	}

	return nil
}
