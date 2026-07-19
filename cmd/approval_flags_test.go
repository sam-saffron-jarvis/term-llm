package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCommonApprovalFlags(t *testing.T) {
	newCommand := func() (*cobra.Command, *string, *bool, *bool) {
		approval := ""
		auto := false
		yolo := false
		command := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
		AddCommonFlags(command, CommonApproval, CommonFlagBindings{Approval: &approval, Auto: &auto, Yolo: &yolo})
		command.SilenceUsage = true
		command.SilenceErrors = true
		return command, &approval, &auto, &yolo
	}

	for _, value := range []string{"prompt", "auto", "yolo"} {
		t.Run("accepts "+value, func(t *testing.T) {
			command, approval, _, _ := newCommand()
			command.SetArgs([]string{"--approval", value})
			if err := command.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if *approval != value || !command.Flags().Changed("approval") {
				t.Fatalf("approval = %q changed=%t, want %q changed", *approval, command.Flags().Changed("approval"), value)
			}
		})
	}

	t.Run("rejects invalid value", func(t *testing.T) {
		command, _, _, _ := newCommand()
		command.SetArgs([]string{"--approval", "automatic"})
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "must be one of prompt, auto, yolo") {
			t.Fatalf("Execute() error = %v", err)
		}
	})

	for _, args := range [][]string{
		{"--approval", "prompt", "--auto"},
		{"--approval", "auto", "--auto"},
		{"--approval", "auto", "--yolo"},
		{"--approval", "prompt", "--auto=false"},
		{"--auto", "--yolo"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			command, _, _, _ := newCommand()
			command.SetArgs(args)
			if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "if any flags in the group") {
				t.Fatalf("Execute() error = %v, want mutually exclusive error", err)
			}
		})
	}

	t.Run("aliases remain detectable", func(t *testing.T) {
		command, _, auto, _ := newCommand()
		command.SetArgs([]string{"--auto"})
		if err := command.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !*auto || !command.Flags().Changed("auto") {
			t.Fatalf("auto = %t changed=%t, want true changed", *auto, command.Flags().Changed("auto"))
		}
	})
}
