package cmd

import (
	"context"
	"fmt"

	"github.com/mbarney/gcalsync/internal/auth"
	"github.com/mbarney/gcalsync/internal/config"
	"github.com/spf13/cobra"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

var calendarAddCmd = &cobra.Command{
	Use:   "add <account> <calendar-id>",
	Short: "Add a calendar to an account",
	Args:  cobra.ExactArgs(2),
	RunE:  runCalendarAdd,
}

func init() {
	calendarCmd.AddCommand(calendarAddCmd)
}

func runCalendarAdd(cmd *cobra.Command, args []string) error {
	accountName, calID := args[0], args[1]

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	acct, exists := cfg.Accounts[accountName]
	if !exists {
		return fmt.Errorf("account %q not found", accountName)
	}

	for _, c := range acct.Calendars {
		if c == calID {
			return fmt.Errorf("calendar %q already exists in account %q", calID, accountName)
		}
	}

	dir := getConfigDir()
	store := &auth.TokenStore{ConfigDir: dir}
	flow := &auth.OAuthFlow{
		ClientID:     cfg.Google.ClientID,
		ClientSecret: cfg.Google.ClientSecret,
		Ports:        cfg.General.AuthorizedPorts,
		TokenStore:   store,
	}

	ctx := context.Background()
	httpClient, err := flow.Client(ctx, accountName)
	if err != nil {
		return err
	}

	svc, err := calendar.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return err
	}

	if _, err := svc.Calendars.Get(calID).Do(); err != nil {
		return fmt.Errorf("cannot access calendar %q: %w", calID, err)
	}

	acct.Calendars = append(acct.Calendars, calID)
	cfg.Accounts[accountName] = acct
	if err := config.Save(dir, cfg); err != nil {
		return err
	}

	fmt.Printf("Calendar %q added to account %q\n", calID, accountName)
	return nil
}
