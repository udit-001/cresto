package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"cresto/internal/config"
)

type stopAction int

const (
	stopKill   stopAction = iota
	stopSkip
	stopStale
	stopLegacy
)

func decideStopAction(info *pidInfo, serverRunning, pidAlive bool) stopAction {
	if info.Port == 0 {
		return stopLegacy
	}
	if serverRunning {
		return stopKill
	}
	if pidAlive {
		return stopSkip
	}
	return stopStale
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the background web UI server",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := readPidFile()
		if err != nil {
			fmt.Println()
			fmt.Println("  No running cresto server found")
			fmt.Println()
			return nil
		}

		serverRunning := info.Port > 0 && isServerRunning(info.Port)
		pidAlive := processAlive(info.PID)

		switch decideStopAction(info, serverRunning, pidAlive) {
		case stopKill, stopLegacy:
			if err := killProcess(info.PID); err != nil {
				return err
			}
			for i := 0; i < 50; i++ {
				if !processAlive(info.PID) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			_ = os.Remove(config.PidPath())
			fmt.Println()
			fmt.Printf("  Server (PID %d) stopped\n", info.PID)
			fmt.Println()

		case stopSkip:
			_ = os.Remove(config.PidPath())
			fmt.Println()
			fmt.Printf("  PID %d is alive but not responding on port %d — it may be a different process.\n", info.PID, info.Port)
			fmt.Println("  Stale PID file cleaned up.")
			fmt.Println()

		case stopStale:
			_ = os.Remove(config.PidPath())
			fmt.Println()
			fmt.Println("  No running server found (stale PID file cleaned up).")
			fmt.Println()
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
