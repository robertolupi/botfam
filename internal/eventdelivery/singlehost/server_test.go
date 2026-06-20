package singlehost_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	contractconnect "github.com/robertolupi/botfam/internal/eventdelivery/contract/connect"
	"github.com/robertolupi/botfam/internal/eventdelivery/singlehost"
)

func TestConnectHandlerServesOverUnixSocket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	path, handler := contractconnect.NewSessionResolverHandler(resolver{})
	mux.Handle(path, handler)

	dir, err := os.MkdirTemp(".", "edv2-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socketPath := filepath.Join(dir, "edv2.sock")
	server, err := singlehost.Serve(ctx, socketPath, mux)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skipf("sandbox does not permit unix socket bind: %v", err)
		}
		t.Fatal(err)
	}
	defer server.HTTPServer.Close()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 2 * time.Second,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://eventdelivery"+contractconnect.SessionResolverResolveProcedure, bytes.NewBufferString(`{"repoOwner":"botfam","repoName":"botfam"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %s, want 200 OK", resp.Status)
	}
}

type resolver struct{}

func (resolver) Resolve(context.Context, *connect.Request[pb.Scope]) (*connect.Response[pb.SessionEndpoint], error) {
	return connect.NewResponse(&pb.SessionEndpoint{Found: true, SessionId: "session-1"}), nil
}
