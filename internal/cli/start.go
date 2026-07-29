package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"cresto/internal/config"
)

var startFlags struct {
	port       int
	foreground bool
	background bool
	daemon     bool
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the web UI server in the background",
	Long: `Start the cresto web UI server as a background daemon.

The server runs detached from the terminal — you can close the shell
and it keeps going. Logs are written to ~/.cresto/server.log.

Use 'cresto stop' to shut it down, or pass --foreground
to run in the foreground instead.

Examples:
  cresto start
  cresto start --port 8080
  cresto start --foreground`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("port") {
			if cfg, err := config.Load(); err == nil && cfg != nil && cfg.Port > 0 {
				startFlags.port = cfg.Port
			}
		}

		if info, err := readPidFile(); err == nil && info.Port > 0 && isServerRunning(info.Port) {
			url := fmt.Sprintf("http://127.0.0.1:%d", info.Port)
			fmt.Println()
			fmt.Printf("  cresto server already running (PID: %d)\n", info.PID)
			fmt.Printf("  %s\n", url)
			fmt.Printf("  Use 'cresto stop' to stop\n")
			fmt.Println()
			return nil
		}

		background := startFlags.background && !startFlags.foreground
		if background && !startFlags.daemon {
			daemonArgs := []string{
				os.Args[0], "start",
				"--port", strconv.Itoa(startFlags.port),
				"--daemon",
			}
			c := exec.Command(daemonArgs[0], daemonArgs[1:]...)
			c.Stdin = nil
			c.Stdout = nil
			c.Stderr = nil
			detachProcess(c)
			if err := c.Start(); err != nil {
				return fmt.Errorf("failed to start background server: %w", err)
			}
			if err := writePidFile(startFlags.port, c.Process.Pid); err != nil {
				return fmt.Errorf("failed to write PID file: %w", err)
			}

			if !waitForServerReady(startFlags.port, 20, 100*time.Millisecond) {
				c.Process.Kill()
				_ = os.Remove(config.PidPath())
				return fmt.Errorf("server failed to start — port may be in use")
			}

			fmt.Println()
			fmt.Printf("  cresto server started in background (PID: %d)\n", c.Process.Pid)
			fmt.Printf("  http://127.0.0.1:%d\n", startFlags.port)
			fmt.Printf("  Use 'cresto stop' to stop\n")
			fmt.Println()
			return nil
		}

		if !startFlags.daemon {
			fmt.Println()
			fmt.Printf("  Starting cresto server...\n")
			fmt.Println()
		}

		cfg := resolveConfig()
		addr := fmt.Sprintf(":%d", startFlags.port)
		return runServer(cfg, addr, startFlags.daemon)
	},
}

func waitForServerReady(port, maxAttempts int, interval time.Duration) bool {
	for i := 0; i < maxAttempts; i++ {
		if isServerRunning(port) {
			return true
		}
		time.Sleep(interval)
	}
	return false
}

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.Flags().IntVar(&startFlags.port, "port", config.DefaultPort, "HTTP server port")
	startCmd.Flags().BoolVarP(&startFlags.foreground, "foreground", "f", false, "Run server in foreground")
	startCmd.Flags().BoolVarP(&startFlags.background, "background", "b", true, "Run server in background")
	startCmd.Flags().BoolVar(&startFlags.daemon, "daemon", false, "")
	startCmd.Flags().MarkHidden("daemon")
}
