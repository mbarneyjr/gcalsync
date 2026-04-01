package cmd

import (
	"fmt"
	"os"

	"github.com/mbarney/gcalsync/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	verbose   bool
	configDir string
)

var rootCmd = &cobra.Command{
	Use:   "gcalsync",
	Short: "Sync Google Calendar events across multiple accounts",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initViper)

	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().StringVar(&configDir, "config-dir", "", "config directory (default ~/.config/gcalsync)")

	viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))
	viper.BindPFlag("config_dir", rootCmd.PersistentFlags().Lookup("config-dir"))
}

func initViper() {
	viper.SetEnvPrefix("GCALSYNC")
	viper.BindEnv("client_id")
	viper.BindEnv("client_secret")
	viper.BindEnv("block_event_visibility")
	viper.BindEnv("authorized_ports")
	viper.BindEnv("ignore_birthdays")
	viper.BindEnv("config_dir")
	viper.AutomaticEnv()
}

func getConfigDir() string {
	if d := viper.GetString("config_dir"); d != "" {
		return d
	}
	return config.DefaultConfigDir()
}

func loadConfig() (*config.Config, error) {
	dir := getConfigDir()
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}

	if id := viper.GetString("client_id"); id != "" {
		cfg.Google.ClientID = id
	}
	if secret := viper.GetString("client_secret"); secret != "" {
		cfg.Google.ClientSecret = secret
	}
	if vis := viper.GetString("block_event_visibility"); vis != "" {
		cfg.General.BlockEventVisibility = vis
	}
	if viper.IsSet("ignore_birthdays") {
		cfg.General.IgnoreBirthdays = viper.GetBool("ignore_birthdays")
	}

	return cfg, nil
}

var accountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage Google accounts",
}

var calendarCmd = &cobra.Command{
	Use:   "calendar",
	Short: "Manage calendars",
}

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage background sync service",
}

func init() {
	rootCmd.AddCommand(accountCmd)
	rootCmd.AddCommand(calendarCmd)
	rootCmd.AddCommand(serviceCmd)
}
