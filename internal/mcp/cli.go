package mcp

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"sort"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
)

// NewMcpCmd builds the `botfam mcp` Cobra command.
func NewMcpCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "mcp",
		Short:         "Interact with the botfam MCP server's resource discovery space",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	c.AddCommand(
		&cobra.Command{
			Use:           "list-resources",
			Short:         "List all advertised MCP resources and templates",
			Args:          cobra.NoArgs,
			SilenceUsage:  true,
			SilenceErrors: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return listResourcesCmd(cmd.OutOrStdout())
			},
		},
		&cobra.Command{
			Use:           "read-resource <uri>",
			Short:         "Read the contents of a discovery resource by URI",
			Args:          cobra.ExactArgs(1),
			SilenceUsage:  true,
			SilenceErrors: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return readResourceCmd(cmd.OutOrStdout(), args[0])
			},
		},
	)

	return c
}

func listResourcesCmd(out io.Writer) error {
	s := &server{
		lockMode: lockActorEnabled(),
	}
	mcpSrv := mcpserver.NewMCPServer(serverName, serverVersion, mcpserver.WithToolCapabilities(false))
	s.mcpSrv = mcpSrv
	s.registerTools(mcpSrv)
	s.registerResources(mcpSrv)

	// Since ListResources only lists actual registered resources, let's collect those.
	resources := mcpSrv.ListResources()
	var sortedURIs []string
	for uri := range resources {
		sortedURIs = append(sortedURIs, uri)
	}
	sort.Strings(sortedURIs)

	fmt.Fprintln(out, "Resources:")
	for _, uri := range sortedURIs {
		res := resources[uri]
		fmt.Fprintf(out, "  - %s: %s\n", res.Resource.URI, res.Resource.Name)
	}

	// We also advertise the resource template "botfam:///skills/{name}"
	fmt.Fprintln(out, "\nTemplates:")
	fmt.Fprintln(out, "  - botfam:///skills/{name}: botfam skill document")

	return nil
}

func readResourceCmd(out io.Writer, uri string) error {
	s := &server{
		lockMode: lockActorEnabled(),
	}
	mcpSrv := mcpserver.NewMCPServer(serverName, serverVersion, mcpserver.WithToolCapabilities(false))
	s.mcpSrv = mcpSrv
	s.registerTools(mcpSrv)
	s.registerResources(mcpSrv)

	// Validate the URI structure
	u, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("invalid URI %q: %w", uri, err)
	}
	if u.Scheme != "botfam" {
		return fmt.Errorf("unsupported scheme %q (expected \"botfam\")", u.Scheme)
	}

	req := mcplib.ReadResourceRequest{}
	req.Params.URI = uri

	contents, err := s.handleReadResource(context.Background(), req)
	if err != nil {
		return err
	}

	if len(contents) == 0 {
		return fmt.Errorf("resource %q returned no content", uri)
	}

	switch tr := contents[0].(type) {
	case mcplib.TextResourceContents:
		_, err := io.WriteString(out, tr.Text)
		return err
	default:
		return fmt.Errorf("unsupported content type: %T", contents[0])
	}
}
