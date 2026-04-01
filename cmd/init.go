package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/mbarney/gcalsync/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize gcalsync configuration",
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	dir := getConfigDir()
	path := config.ConfigPath(dir)

	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Config already exists at %s\n", path)
		return nil
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Google OAuth client ID: ")
	clientID, _ := reader.ReadString('\n')
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return fmt.Errorf("client ID is required")
	}

	fmt.Print("Google OAuth client secret: ")
	clientSecret, _ := reader.ReadString('\n')
	clientSecret = strings.TrimSpace(clientSecret)
	if clientSecret == "" {
		return fmt.Errorf("client secret is required")
	}

	cfg := &config.Config{
		Google: config.GoogleConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
		},
		General: config.GeneralConfig{
			BlockEventVisibility: "private",
			AuthorizedPorts:      []int{8080, 8081, 8082},
			IgnoreBirthdays:      true,
		},
		Accounts: make(map[string]config.AccountConfig),
	}

	if err := config.Save(dir, cfg); err != nil {
		return err
	}

	fmt.Printf("Config written to %s\n", path)
	fmt.Println("Run `gcalsync account add <name>` to add your first account")
	return nil
}
