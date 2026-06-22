package contractconnect

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
)

type SessionResolverClient interface {
	Resolve(context.Context, *connect.Request[pb.Scope]) (*connect.Response[pb.SessionEndpoint], error)
}

type sessionResolverClient struct {
	resolve *connect.Client[pb.Scope, pb.SessionEndpoint]
}

func NewSessionResolverClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) SessionResolverClient {
	return sessionResolverClient{
		resolve: connect.NewClient[pb.Scope, pb.SessionEndpoint](httpClient, baseURL+SessionResolverResolveProcedure, opts...),
	}
}

func (c sessionResolverClient) Resolve(ctx context.Context, req *connect.Request[pb.Scope]) (*connect.Response[pb.SessionEndpoint], error) {
	return c.resolve.CallUnary(ctx, req)
}

type WorkerChannelClient interface {
	ProposeForgeAction(context.Context, *connect.Request[pb.ForgeAction]) (*connect.Response[pb.ActionAck], error)
}

type workerChannelClient struct {
	proposeForgeAction *connect.Client[pb.ForgeAction, pb.ActionAck]
}

func NewWorkerChannelClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) WorkerChannelClient {
	return workerChannelClient{
		proposeForgeAction: connect.NewClient[pb.ForgeAction, pb.ActionAck](httpClient, baseURL+WorkerChannelProposeForgeActionProcedure, opts...),
	}
}

func (c workerChannelClient) ProposeForgeAction(ctx context.Context, req *connect.Request[pb.ForgeAction]) (*connect.Response[pb.ActionAck], error) {
	return c.proposeForgeAction.CallUnary(ctx, req)
}

var _ connect.HTTPClient = (*http.Client)(nil)
