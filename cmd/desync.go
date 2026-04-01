package cmd

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/mbarney/gcalsync/internal/auth"
	"github.com/mbarney/gcalsync/internal/gcal"
	syncpkg "github.com/mbarney/gcalsync/internal/sync"
	"github.com/spf13/cobra"
)

var desyncCalendar string

var desyncCmd = &cobra.Command{
	Use:   "desync",
	Short: "Remove all gcalsync blocker events",
	RunE:  runDesync,
}

func init() {
	desyncCmd.Flags().StringVar(&desyncCalendar, "calendar", "", "only desync a specific calendar ID")
	rootCmd.AddCommand(desyncCmd)
}

func runDesync(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx := context.Background()
	dir := getConfigDir()
	store := &auth.TokenStore{ConfigDir: dir}
	flow := &auth.OAuthFlow{
		ClientID:     cfg.Google.ClientID,
		ClientSecret: cfg.Google.ClientSecret,
		Ports:        cfg.General.AuthorizedPorts,
		TokenStore:   store,
	}

	clients := make(map[string]*gcal.Client)
	for accountName := range cfg.Accounts {
		httpClient, err := flow.Client(ctx, accountName)
		if err != nil {
			log.Printf("error: %s: %v", accountName, err)
			continue
		}
		client, err := gcal.NewClient(ctx, httpClient, accountName)
		if err != nil {
			log.Printf("error: %s: %v", accountName, err)
			continue
		}
		clients[accountName] = client
	}

	engine := &syncpkg.Engine{
		Config:  cfg,
		Clients: clients,
		Verbose: verbose,
	}

	plan := engine.DesyncPlan(ctx, desyncCalendar)
	plan.PrintPlan()

	if plan.IsEmpty() {
		fmt.Println("gcalsync: no blocker events to remove")
		return nil
	}

	fmt.Println("\nApplying...")
	result := engine.Apply(ctx, plan)

	fmt.Printf("gcalsync: desync complete (%d removed)\n", result.Deleted)

	if len(result.Errors) > 0 {
		fmt.Printf("gcalsync: %d errors during desync:\n", len(result.Errors))
		for _, e := range result.Errors {
			fmt.Printf("  %s: %v\n", e.Calendar, e.Err)
		}
		os.Exit(1)
	}
	return nil
}
