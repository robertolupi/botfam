package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"connectrpc.com/connect"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	"github.com/robertolupi/botfam/internal/eventdelivery/singlehost"
	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/spf13/cobra"
)

// NewSprintCmd builds the `botfam sprint` Cobra command and its subcommands.
func NewSprintCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "sprint",
		Short:         "Manage sprint sessions (M1 skeleton)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	c.AddCommand(newSprintStartCmd())
	c.AddCommand(newSprintRunCmd())
	c.AddCommand(newSprintEndCmd())
	c.AddCommand(newSprintLsCmd())
	c.AddCommand(newSprintUiCmd())

	return c
}

func newSprintStartCmd() *cobra.Command {
	var milestone int64
	var issuesStr string

	cmd := &cobra.Command{
		Use:   "start $ID",
		Short: "Start a sprint session with a milestone or specific issues",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if milestone == 0 && issuesStr == "" {
				return errors.New("must specify either --milestone N or --issues N1,N2")
			}

			var issues []string
			if issuesStr != "" {
				issues = strings.Split(issuesStr, ",")
				for i, issue := range issues {
					issues[i] = strings.TrimSpace(issue)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sprint start placeholder: ID=%s, Milestone=%d, Issues=%v\n", id, milestone, issues)
			return nil
		},
	}

	cmd.Flags().Int64Var(&milestone, "milestone", 0, "Milestone number")
	cmd.Flags().StringVar(&issuesStr, "issues", "", "Comma-separated list of issue numbers")
	return cmd
}

func newSprintRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run $ID",
		Short: "Run the sprint supervisor (acquires lease)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			repoName := famconfig.ResolveRepoName(wd)
			if repoName == "" {
				return errors.New("could not resolve repository name from current directory")
			}

			lease := singlehost.NewLease()
			defer lease.Close()

			grant, err := lease.Acquire(cmd.Context(), &connect.Request[pb.AcquireRequest]{
				Msg: &pb.AcquireRequest{
					Scope: &pb.Scope{
						RepoName: repoName,
					},
					HolderIdentity: "supervisor",
				},
			})
			if err != nil {
				return fmt.Errorf("lease acquisition failed: %w", err)
			}

			if !grant.Msg.GetGranted() {
				return fmt.Errorf("sprint run: lease is busy or held by another live process")
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Sprint run started: lease acquired for session %s (fencing_token=%d)\n", id, grant.Msg.GetFencingToken())

			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

			select {
			case <-cmd.Context().Done():
			case <-sigChan:
			}

			// Clean release
			_, _ = lease.Release(context.Background(), &connect.Request[pb.ReleaseRequest]{
				Msg: &pb.ReleaseRequest{
					LeaseId: grant.Msg.GetLeaseId(),
				},
			})

			fmt.Fprintln(cmd.OutOrStdout(), "Sprint run stopped.")
			return nil
		},
	}
	return cmd
}

func newSprintEndCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "end $ID",
		Short: "End a sprint session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			fmt.Fprintf(cmd.OutOrStdout(), "Sprint end placeholder: ID=%s\n", id)
			return nil
		},
	}
}

func newSprintLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List sprint sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "Sprint list placeholder")
			return nil
		},
	}
}

func newSprintUiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui $ID",
		Short: "Inspect a running or past session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			fmt.Fprintf(cmd.OutOrStdout(), "Sprint UI placeholder: ID=%s\n", id)
			return nil
		},
	}
}
