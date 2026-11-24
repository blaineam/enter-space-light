package rclonecli

import (
	"os"

	"github.com/rclone/rclone/cmd"
)

// Run executes rclone with args (everything after `esl rclone`) and exits.
// configPath, if set, pins rclone to the same rclone.conf the daemon uses.
func Run(args []string, configPath string) {
	if configPath != "" {
		_ = os.Setenv("RCLONE_CONFIG", configPath)
	}
	os.Args = append([]string{"esl rclone"}, args...)
	cmd.Main()
	os.Exit(0)
}
