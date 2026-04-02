package cmd

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/mbarney/gcalsync/internal/auth"
	"github.com/mbarney/gcalsync/internal/gcal"
	"github.com/mbarney/gcalsync/internal/service"
	syncpkg "github.com/mbarney/gcalsync/internal/sync"
	"github.com/spf13/cobra"
)

var dryRun bool

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync calendar events across accounts",
	RunE:  runSync,
}

func init() {
	syncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would happen without making changes")
	rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	if len(cfg.Accounts) == 0 {
		return fmt.Errorf("no accounts configured — run `gcalsync account add <name>` first")
	}

	totalCals := 0
	for _, acct := range cfg.Accounts {
		totalCals += len(acct.Calendars)
	}

	fmt.Printf("gcalsync: syncing %d accounts, %d calendars\n", len(cfg.Accounts), totalCals)

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

	plan := engine.Plan(ctx)
	plan.PrintPlan(verbose)

	if dryRun || plan.IsEmpty() {
		if plan.IsEmpty() {
			fmt.Println("gcalsync: no changes needed")
			service.WriteLastSuccess(dir)
		}
		return nil
	}

	fmt.Println("\nApplying...")
	result := engine.Apply(ctx, plan)

	fmt.Printf("gcalsync: %d created, %d updated, %d deleted\n", result.Created, result.Updated, result.Deleted)

	if len(result.Errors) == 0 {
		service.WriteLastSuccess(dir)
	}

	if len(result.Errors) > 0 {
		fmt.Printf("gcalsync: %d errors during apply:\n", len(result.Errors))
		for _, e := range result.Errors {
			fmt.Printf("  %s: %v\n", e.Calendar, e.Err)
		}
		os.Exit(1)
	}
	return nil
}
