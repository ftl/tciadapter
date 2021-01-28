package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
)

var rootFlags = struct {
	reconnect *bool
}{}

var rootCmd = &cobra.Command{
	Use:   "tciadapter hamlib_address tci_host",
	Short: "An adapter to connect Hamlib clients to TCI servers.",
	Run:   root,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootFlags.reconnect = rootCmd.PersistentFlags().BoolP("reconnect", "r", false, "Automatically try to reconnect if the connection fails")
	rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func root(cmd *cobra.Command, args []string) {
	log.Print("TCI-Hamlib Adapter")
}
