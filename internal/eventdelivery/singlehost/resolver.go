package singlehost

import (
	"context"
	"os"

	"connectrpc.com/connect"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/pelletier/go-toml/v2"
)

// SessionResolver implements pb.SessionResolver via connect.
type SessionResolver struct{}

// NewSessionResolver creates a new single-host SessionResolver.
func NewSessionResolver() *SessionResolver {
	return &SessionResolver{}
}

// Resolve reads the session file for the scope's repository and returns the active endpoint if live.
func (s *SessionResolver) Resolve(ctx context.Context, req *connect.Request[pb.Scope]) (*connect.Response[pb.SessionEndpoint], error) {
	scope := req.Msg
	repoName := scope.GetRepoName()
	if repoName == "" {
		// Fallback to deriving repo name from cwd
		if wd, err := os.Getwd(); err == nil {
			repoName = famconfig.ResolveRepoName(wd)
		}
	}

	if repoName == "" {
		return connect.NewResponse(&pb.SessionEndpoint{Found: false}), nil
	}

	path, err := ConfigPath(repoName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return connect.NewResponse(&pb.SessionEndpoint{Found: false}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var sf SessionFile
	if err := toml.Unmarshal(data, &sf); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Verify the process is still live
	if !isProcessLive(sf.PID) {
		return connect.NewResponse(&pb.SessionEndpoint{Found: false}), nil
	}

	return connect.NewResponse(&pb.SessionEndpoint{
		Found:        true,
		Address:      sf.Addr,
		Token:        sf.Token,
		SessionId:    sf.LeaseID,
		FencingToken: sf.FencingToken,
	}), nil
}
