//go:build !linux || !amd64 || !rustproxy

package cmd

import (
	"time"

	"github.com/spf13/cobra"
	appproxy "github.com/term-llm/term-llm/internal/app/proxy"
)

func init() { serveCmd.AddCommand(newServeProxyCommand()) }
func newServeProxyCommand() *cobra.Command {
	opts := appproxy.Options{}
	c := &cobra.Command{Use: "proxy", Short: "Run a standalone configuration-driven credential proxy", Args: cobra.NoArgs, Annotations: map[string]string{"isolated-startup": "true"}, PersistentPreRunE: proxyPreRun, RunE: func(c *cobra.Command, _ []string) error { return appproxy.Run(c.Context(), opts, c.OutOrStdout()) }}
	c.Flags().StringVar(&opts.StateDir, "state-dir", "", "Private proxy state directory (default: user config directory/term-llm/proxy; separate from agent state)")
	c.Flags().StringVar(&opts.DebugPayloadDir, "debug-payload-dir", "", "Start raw payload capture in this private directory (also controllable in the Debug UI)")
	c.Flags().Int64Var(&opts.DebugPayloadMaxBytes, "debug-payload-max-bytes", 4<<20, "Maximum bytes per captured body or WebSocket direction")
	c.Flags().Int64Var(&opts.DebugPayloadMaxTotalBytes, "debug-payload-max-total-bytes", appproxy.DefaultDebugPayloadMaxTotalBytes, "Maximum retained capture-file bytes in the debug directory (also limited to 1,000 requests)")
	c.Flags().StringVar(&opts.Auth, "auth", "passkey", "Browser admin authentication: passkey or token (explicit legacy/local mode)")
	c.Flags().StringVar(&opts.PasskeyBootstrapTokenFile, "passkey-bootstrap-token-file", "", "Private one-use enrollment secret file (created automatically for local setup)")
	c.Flags().StringVar(&opts.PasskeyRecoveryTokenFile, "passkey-recovery-token-file", "", "Private one-use passkey recovery secret file (existing enrollment only)")
	c.Flags().StringVar(&opts.AdminTokenFile, "admin-token-file", "", "Private administrator token file (only with --auth token)")
	c.Flags().StringVar(&opts.ConfigFile, "config", "", "Operator-owned route YAML; omit for editable UI-managed routes")
	c.Flags().StringVar(&opts.PublicURL, "public-url", "", "Browser HTTPS origin for remote access (default: http://localhost:<port> for loopback listeners)")
	c.Flags().StringVar(&opts.Host, "host", "127.0.0.1", "Listen host")
	c.Flags().IntVar(&opts.Port, "port", 8082, "Listen port")
	c.Flags().IntVar(&opts.MaxConcurrent, "max-concurrent", 64, "Maximum active forwarding requests (no queue)")
	c.Flags().Int64Var(&opts.MaxBody, "max-body-bytes", 64<<20, "Maximum streamed request body bytes")
	c.Flags().DurationVar(&opts.RequestTimeout, "request-timeout", 15*time.Minute, "Total request deadline (including streaming)")
	c.Flags().IntVar(&opts.RetentionDays, "retention-days", 30, "Request metadata retention in days (batched deletion)")
	var host string
	var port int
	health := &cobra.Command{Use: "health", Short: "Probe local proxy/store readiness without contacting upstreams", Args: cobra.NoArgs, Annotations: map[string]string{"isolated-startup": "true"}, RunE: func(c *cobra.Command, _ []string) error { return appproxy.Health(c.Context(), host, port) }}
	health.Flags().StringVar(&host, "host", "127.0.0.1", "Loopback readiness host")
	health.Flags().IntVar(&port, "port", 8082, "Readiness port")
	c.AddCommand(health)
	return c
}
