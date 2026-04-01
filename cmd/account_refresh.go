package cmd

import (
	"context"
	"fmt"

	"github.com/mbarney/gcalsync/internal/auth"
	"github.com/spf13/cobra"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

var accountRefreshCmd = &cobra.Command{
	Use:   "refresh <name>",
	Short: "Re-authenticate an account",
	Args:  cobra.ExactArgs(1),
	RunE:  runAccountRefresh,
}

func init() {
	accountCmd.AddCommand(accountRefreshCmd)
}

func runAccountRefresh(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	if _, exists := cfg.Accounts[name]; !exists {
		return fmt.Errorf("account %q not found", name)
	}

	dir := getConfigDir()
	store := &auth.TokenStore{ConfigDir: dir}
	store.Delete(name)

	flow := &auth.OAuthFlow{
		ClientID:     cfg.Google.ClientID,
		ClientSecret: cfg.Google.ClientSecret,
		Ports:        cfg.General.AuthorizedPorts,
		TokenStore:   store,
	}

	fmt.Printf("Re-authenticating account %q...\n", name)
	if _, err := flow.Authenticate(name); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	ctx := context.Background()
	httpClient, err := flow.Client(ctx, name)
	if err != nil {
		return fmt.Errorf("creating client: %w", err)
	}

	svc, err := calendar.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return fmt.Errorf("creating calendar service: %w", err)
	}

	list, err := svc.CalendarList.List().Do()
	if err != nil {
		return fmt.Errorf("verifying access: %w", err)
	}

	fmt.Printf("Account %q refreshed — access verified (%d calendars)\n", name, len(list.Items))
	return nil
}
