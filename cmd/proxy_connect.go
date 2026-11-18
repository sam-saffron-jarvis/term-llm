package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/term-llm/term-llm/internal/app/proxyconnect"
	"github.com/term-llm/term-llm/internal/app/proxyrun"
	"github.com/term-llm/term-llm/internal/exitcode"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

func proxyPreRun(_ *cobra.Command, _ []string) error {
	return proxyclient.ValidateProfiling(debugRaw, cpuProfile, memProfile, pprofFlag)
}

func init() { rootCmd.AddCommand(newProxyCommand()) }

func newProxyCommand() *cobra.Command {
	root := &cobra.Command{
		Use: "proxy", Short: "Run native clients with credentials held by a proxy",
		PersistentPreRunE: proxyPreRun, Annotations: map[string]string{"isolated-startup": "true"},
	}
	run := &cobra.Command{Use: "run", Short: "Run a native client with process-local proxy settings"}
	var opts proxyrun.Options
	claude := &cobra.Command{
		Use:   "claude [-- native args...]",
		Short: "Run Claude Code behind the credential proxy",
		Long: `Run Claude Code with a process-local proxy environment and an isolated
term-managed Claude profile. Your normal ~/.claude login and settings are untouched.

Put every native Claude argument after -- so term-llm flags cannot consume it:
  term-llm proxy run claude -- --resume
  term-llm proxy run claude -- -p "summarize this repo"`,
		Args: cobra.ArbitraryArgs, Annotations: map[string]string{"isolated-startup": "true"},
		RunE: func(c *cobra.Command, args []string) error {
			opts.Args = args
			opts.In, opts.Out, opts.Err = c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr()
			err := proxyrun.Run(c.Context(), opts)
			var childExit *proxyrun.ChildExitError
			if errors.As(err, &childExit) {
				message := fmt.Sprintf("claude exited with status %d", childExit.Code)
				if childExit.Code == exitcode.Cancelled {
					message = "claude interrupted"
				}
				return exitcode.ExitError{Code: childExit.Code, Message: message}
			}
			return err
		},
	}
	claude.Flags().StringVar(&opts.URL, "url", "", "Exact scoped Claude proxy binding root (default: configured claude-bin binding)")
	claude.Flags().StringVar(&opts.Token, "token", "", "Scoped proxy client token for --url (never a Claude vendor credential)")
	claude.Flags().StringVar(&opts.Command, "claude-command", "claude", "Claude Code executable to launch")
	run.AddCommand(claude)
	disconnect := &cobra.Command{Use: "disconnect", Short: "Undo a legacy connector's Claude-only gateway settings"}
	undo := &cobra.Command{
		Use: "claude", Short: "Restore legacy connector-owned Claude settings without overwriting unrelated settings",
		Args: cobra.NoArgs, Annotations: map[string]string{"isolated-startup": "true"},
		RunE: func(c *cobra.Command, _ []string) error {
			return proxyconnect.Disconnect(proxyconnect.Options{Out: c.OutOrStdout()})
		},
	}
	disconnect.AddCommand(undo)
	root.AddCommand(run, disconnect)
	return root
}
