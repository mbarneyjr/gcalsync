package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mbarney/gcalsync/internal/auth"
	"github.com/mbarney/gcalsync/internal/config"
	"github.com/mbarney/gcalsync/internal/gcal"
	"github.com/spf13/cobra"
	"google.golang.org/api/calendar/v3"
)

var (
	inspectEventID         string
	inspectQuery           string
	inspectSingleEvents    bool
	inspectAll             bool
	inspectIncludeCancelled bool
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect raw Google Calendar data",
}

var inspectAccountsCmd = &cobra.Command{
	Use:   "accounts",
	Short: "List all accounts and their calendars",
	RunE:  runInspectAccounts,
}

var inspectEventsCmd = &cobra.Command{
	Use:   "events <account> <calendar-id>",
	Short: "List raw events from a Google Calendar",
	Args:  cobra.ExactArgs(2),
	RunE:  runInspectEvents,
}

func init() {
	inspectEventsCmd.Flags().StringVar(&inspectEventID, "event-id", "", "get a single event by ID")
	inspectEventsCmd.Flags().StringVar(&inspectQuery, "query", "", "free-text search query")
	inspectEventsCmd.Flags().BoolVar(&inspectSingleEvents, "single-events", false, "expand recurring events into instances")
	inspectEventsCmd.Flags().BoolVar(&inspectAll, "all", false, "no time window (default: 2-month window)")
	inspectEventsCmd.Flags().BoolVar(&inspectIncludeCancelled, "include-cancelled", false, "include cancelled/deleted events")

	inspectCmd.AddCommand(inspectEventsCmd)
	inspectCmd.AddCommand(inspectAccountsCmd)
	rootCmd.AddCommand(inspectCmd)
}

func runInspectAccounts(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	type accountInfo struct {
		Name       string   `json:"name"`
		Enterprise bool     `json:"enterprise"`
		Calendars  []string `json:"calendars"`
	}

	var accounts []accountInfo
	for name, acct := range cfg.Accounts {
		accounts = append(accounts, accountInfo{
			Name:       name,
			Enterprise: acct.Enterprise,
			Calendars:  acct.Calendars,
		})
	}

	return printJSON(accounts)
}

func runInspectEvents(cmd *cobra.Command, args []string) error {
	accountName := args[0]
	calendarID := args[1]

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if _, ok := cfg.Accounts[accountName]; !ok {
		return fmt.Errorf("account %q not found in config", accountName)
	}

	ctx := context.Background()
	client, err := buildClient(ctx, cfg, accountName)
	if err != nil {
		return err
	}

	// Single event lookup
	if inspectEventID != "" {
		ev, err := client.Service.Events.Get(calendarID, inspectEventID).Do()
		if err != nil {
			return fmt.Errorf("getting event: %w", err)
		}
		return printJSON(ev)
	}

	// List events
	var all []*calendar.Event
	pageToken := ""
	for {
		call := client.Service.Events.List(calendarID).
			SingleEvents(inspectSingleEvents).
			MaxResults(2500)

		if !inspectAll {
			now := time.Now()
			timeMin := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
			timeMax := timeMin.AddDate(0, 2, 0)
			call = call.TimeMin(timeMin.Format(time.RFC3339)).
				TimeMax(timeMax.Format(time.RFC3339))
		}

		if inspectQuery != "" {
			call = call.Q(inspectQuery)
		}

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		result, err := call.Do()
		if err != nil {
			return fmt.Errorf("listing events: %w", err)
		}

		for _, ev := range result.Items {
			if !inspectIncludeCancelled && ev.Status == "cancelled" {
				continue
			}
			all = append(all, ev)
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}

	return printJSON(map[string]any{
		"calendar": calendarID,
		"count":    len(all),
		"events":   all,
	})
}

func buildClient(ctx context.Context, cfg *config.Config, accountName string) (*gcal.Client, error) {
	dir := getConfigDir()
	store := &auth.TokenStore{ConfigDir: dir}
	flow := &auth.OAuthFlow{
		ClientID:     cfg.Google.ClientID,
		ClientSecret: cfg.Google.ClientSecret,
		Ports:        cfg.General.AuthorizedPorts,
		TokenStore:   store,
	}

	httpClient, err := flow.Client(ctx, accountName)
	if err != nil {
		return nil, fmt.Errorf("auth for %s: %w", accountName, err)
	}

	return gcal.NewClient(ctx, httpClient, accountName)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
