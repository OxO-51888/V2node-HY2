package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version  = "TempVersion" //use ldflags replace
	codename = "v2node"
	intro    = "A V2board HY2-only backend based on official Hysteria2"
)

var versionCommand = cobra.Command{
	Use:   "version",
	Short: "Print version info",
	Run: func(_ *cobra.Command, _ []string) {
		showVersion()
	},
}

func init() {
	command.AddCommand(&versionCommand)
}

func showVersion() {
	fmt.Printf("%s %s (%s) \n", codename, version, intro)
}
