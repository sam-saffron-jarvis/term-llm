package cmd

import (
	"github.com/spf13/cobra"
	"github.com/term-llm/term-llm/internal/app/proxyconnect"
)

func init() { rootCmd.AddCommand(newProxyCommand()) }
func newProxyCommand() *cobra.Command {
	root := &cobra.Command{Use: "proxy", Short: "Connect standalone clients to a credential proxy", PersistentPreRunE: proxyPreRun, Annotations: map[string]string{"isolated-startup": "true"}}
	connect := &cobra.Command{Use: "connect", Short: "Pair a standalone client with browser administrator consent"}
	opts := proxyconnect.Options{}
	claude := &cobra.Command{Use: "claude", Short: "Pair Claude Code; keep subscription credentials on the proxy", Args: cobra.NoArgs, Annotations: map[string]string{"isolated-startup": "true"}, RunE: func(c *cobra.Command, _ []string) error {
		opts.In = c.InOrStdin()
		opts.Out = c.OutOrStdout()
		return proxyconnect.Run(c.Context(), opts)
	}}
	claude.Flags().StringVar(&opts.URL, "url", "", "Exact proxy HTTPS origin (saved connector URL or prompt when omitted)")
	claude.Flags().StringVar(&opts.Account, "account", "", "Claude connection name (default: claude, or this connector's existing account)")
	claude.Flags().StringVar(&opts.ClientName, "name", "", "Name of this scoped client (default: hostname)")
	claude.Flags().BoolVar(&opts.Reconnect, "reconnect", false, "Explicitly replace the Claude subscription token after browser approval; existing clients keep their grants")
	claude.Flags().BoolVar(&opts.PasteToken, "paste-token", false, "Use masked subscription-token paste instead of official claude setup-token, only if approved setup/reconnect needs it")
	claude.Flags().StringVar(&opts.ClaudeCommand, "claude-command", "claude", "Official Claude executable to run for setup-token")
	claude.Flags().BoolVar(&opts.NoBrowser, "no-browser", false, "Print the proxy approval link without opening it automatically")
	claude.Flags().BoolVar(&opts.Cancel, "cancel", false, "Cancel this user's existing pending enrollment; never revoke or remint a committed client")
	connect.AddCommand(claude)
	disconnect := &cobra.Command{Use: "disconnect", Short: "Undo a standalone client's local gateway settings"}
	undo := &cobra.Command{Use: "claude", Short: "Restore Claude-only gateway settings without overwriting unrelated settings", Args: cobra.NoArgs, Annotations: map[string]string{"isolated-startup": "true"}, RunE: func(c *cobra.Command, _ []string) error {
		return proxyconnect.Disconnect(proxyconnect.Options{In: c.InOrStdin(), Out: c.OutOrStdout()})
	}}
	disconnect.AddCommand(undo)
	root.AddCommand(connect, disconnect)
	return root
}
