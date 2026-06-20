package workerchannel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	contractconnect "github.com/robertolupi/botfam/internal/eventdelivery/contract/connect"
	"github.com/robertolupi/botfam/internal/eventdelivery/store"
	"google.golang.org/protobuf/types/known/emptypb"
)

const FencingTokenHeader = "Botfam-Fencing-Token"

type ForgeExecutor interface {
	ExecuteForgeAction(ctx context.Context, toolName, argumentsJSON string) (responseJSON string, err error)
}

type ForgeExecutorFunc func(context.Context, string, string) (string, error)

func (f ForgeExecutorFunc) ExecuteForgeAction(ctx context.Context, toolName, argumentsJSON string) (string, error) {
	return f(ctx, toolName, argumentsJSON)
}

type Service struct {
	DB       *sql.DB
	Executor ForgeExecutor
}

func (s Service) Handler(opts ...connect.HandlerOption) (string, http.Handler) {
	return contractconnect.NewWorkerChannelHandler(s, opts...)
}

func (s Service) DispatchWork(ctx context.Context, _ *connect.Request[pb.WorkerStream], stream *connect.ServerStream[pb.WorkItem]) error {
	if s.DB == nil {
		return errors.New("workerchannel: db is required")
	}
	items, err := store.PendingWorkItems(ctx, s.DB, 100)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := stream.Send(&pb.WorkItem{
			Id:              item.ID,
			Kind:            item.Kind,
			SourceId:        item.SourceID,
			Title:           item.Title,
			Body:            item.Body,
			ScopeGeneration: item.ScopeGeneration,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) RecordArtifact(ctx context.Context, req *connect.Request[pb.Artifact]) (*connect.Response[emptypb.Empty], error) {
	if s.DB == nil {
		return nil, errors.New("workerchannel: db is required")
	}
	msg := req.Msg
	if err := store.RecordArtifact(ctx, s.DB, "", msg.GetWorkItemId(), msg.GetKind(), msg.GetUri(), msg.GetSha256()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (s Service) SubmitTelemetry(ctx context.Context, stream *connect.ClientStream[pb.Span]) (*connect.Response[emptypb.Empty], error) {
	if s.DB == nil {
		return nil, errors.New("workerchannel: db is required")
	}
	for stream.Receive() {
		span := stream.Msg()
		ts := ""
		if span.GetTimestamp() != nil {
			ts = span.GetTimestamp().AsTime().UTC().Format("2006-01-02T15:04:05.000Z")
		}
		if err := store.RecordTelemetrySpan(ctx, s.DB, span.GetTraceId(), span.GetSpanId(), span.GetComponent(), span.GetEventType(), span.GetPayloadJson(), ts); err != nil {
			return nil, err
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (s Service) ProposeForgeAction(ctx context.Context, req *connect.Request[pb.ForgeAction]) (*connect.Response[pb.ActionAck], error) {
	if s.DB == nil {
		return nil, errors.New("workerchannel: db is required")
	}
	msg := req.Msg
	fencingToken, err := parseFencingToken(req.Header())
	if err != nil {
		return nil, err
	}
	outboxID := outboxID(msg.GetWorkItemId(), msg.GetActionKey())
	res, err := store.EnqueueForgeAction(ctx, s.DB, outboxID, msg.GetWorkItemId(), msg.GetActionKey(), msg.GetToolName(), msg.GetArgumentsJson(), fencingToken)
	if err != nil {
		return nil, err
	}
	if res.Deduped && res.Committed {
		return connect.NewResponse(&pb.ActionAck{Committed: res.Committed, Deduped: true, OutboxId: res.ID, ResponseJson: defaultJSON(res.ResponseJSON)}), nil
	}
	deduped := res.Deduped
	if s.Executor == nil {
		errJSON := `{"error":"workerchannel: forge executor is required"}`
		_ = store.RecordForgeActionAttempt(ctx, s.DB, res.ID, fencingToken, "error", errJSON)
		return nil, errors.New("workerchannel: forge executor is required")
	}
	responseJSON, execErr := s.Executor.ExecuteForgeAction(ctx, msg.GetToolName(), msg.GetArgumentsJson())
	if execErr != nil {
		errJSON := fmt.Sprintf(`{"error":%q}`, execErr.Error())
		_ = store.RecordForgeActionAttempt(ctx, s.DB, res.ID, fencingToken, "error", errJSON)
		return nil, execErr
	}
	responseJSON = defaultJSON(responseJSON)
	if err := store.RecordForgeActionAttempt(ctx, s.DB, res.ID, fencingToken, "committed", responseJSON); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.ActionAck{Committed: true, Deduped: deduped, OutboxId: res.ID, ResponseJson: responseJSON}), nil
}

func parseFencingToken(h http.Header) (uint64, error) {
	raw := strings.TrimSpace(h.Get(FencingTokenHeader))
	if raw == "" {
		return 0, nil
	}
	token, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s header: %w", FencingTokenHeader, err)
	}
	return token, nil
}

func outboxID(workItemID, actionKey string) string {
	return "forge-action:" + workItemID + ":" + actionKey
}

func defaultJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}
