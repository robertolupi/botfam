package contractconnect

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	SessionResolverName             = "botfam.eventdelivery.v2.SessionResolver"
	SessionResolverResolveProcedure = "/botfam.eventdelivery.v2.SessionResolver/Resolve"

	LeaseName             = "botfam.eventdelivery.v2.Lease"
	LeaseAcquireProcedure = "/botfam.eventdelivery.v2.Lease/Acquire"
	LeaseRenewProcedure   = "/botfam.eventdelivery.v2.Lease/Renew"
	LeaseReleaseProcedure = "/botfam.eventdelivery.v2.Lease/Release"

	WorkerChannelName                        = "botfam.eventdelivery.v2.WorkerChannel"
	WorkerChannelDispatchWorkProcedure       = "/botfam.eventdelivery.v2.WorkerChannel/DispatchWork"
	WorkerChannelRecordArtifactProcedure     = "/botfam.eventdelivery.v2.WorkerChannel/RecordArtifact"
	WorkerChannelSubmitTelemetryProcedure    = "/botfam.eventdelivery.v2.WorkerChannel/SubmitTelemetry"
	WorkerChannelProposeForgeActionProcedure = "/botfam.eventdelivery.v2.WorkerChannel/ProposeForgeAction"

	ObservationPipelineName                     = "botfam.eventdelivery.v2.ObservationPipeline"
	ObservationPipelineObserveProcedure         = "/botfam.eventdelivery.v2.ObservationPipeline/Observe"
	ObservationPipelineTranslateProcedure       = "/botfam.eventdelivery.v2.ObservationPipeline/Translate"
	ObservationPipelineRecordProcessedProcedure = "/botfam.eventdelivery.v2.ObservationPipeline/RecordProcessed"

	SessionStoreName                            = "botfam.eventdelivery.v2.SessionStore"
	SessionStorePutSessionDescriptorProcedure   = "/botfam.eventdelivery.v2.SessionStore/PutSessionDescriptor"
	SessionStoreListSessionDescriptorsProcedure = "/botfam.eventdelivery.v2.SessionStore/ListSessionDescriptors"
)

type SessionResolverHandler interface {
	Resolve(context.Context, *connect.Request[pb.Scope]) (*connect.Response[pb.SessionEndpoint], error)
}

func NewSessionResolverHandler(svc SessionResolverHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	methods := pb.File_botfam_eventdelivery_v2_eventdelivery_proto.Services().ByName("SessionResolver").Methods()
	resolve := connect.NewUnaryHandler(
		SessionResolverResolveProcedure,
		svc.Resolve,
		connect.WithSchema(methods.ByName("Resolve")),
		connect.WithHandlerOptions(opts...),
	)
	return "/botfam.eventdelivery.v2.SessionResolver/", route(map[string]http.Handler{
		SessionResolverResolveProcedure: resolve,
	})
}

type LeaseHandler interface {
	Acquire(context.Context, *connect.Request[pb.AcquireRequest]) (*connect.Response[pb.Grant], error)
	Renew(context.Context, *connect.Request[pb.RenewRequest]) (*connect.Response[pb.Grant], error)
	Release(context.Context, *connect.Request[pb.ReleaseRequest]) (*connect.Response[emptypb.Empty], error)
}

func NewLeaseHandler(svc LeaseHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	methods := pb.File_botfam_eventdelivery_v2_eventdelivery_proto.Services().ByName("Lease").Methods()
	acquire := connect.NewUnaryHandler(LeaseAcquireProcedure, svc.Acquire, connect.WithSchema(methods.ByName("Acquire")), connect.WithHandlerOptions(opts...))
	renew := connect.NewUnaryHandler(LeaseRenewProcedure, svc.Renew, connect.WithSchema(methods.ByName("Renew")), connect.WithHandlerOptions(opts...))
	release := connect.NewUnaryHandler(LeaseReleaseProcedure, svc.Release, connect.WithSchema(methods.ByName("Release")), connect.WithHandlerOptions(opts...))
	return "/botfam.eventdelivery.v2.Lease/", route(map[string]http.Handler{
		LeaseAcquireProcedure: acquire,
		LeaseRenewProcedure:   renew,
		LeaseReleaseProcedure: release,
	})
}

type WorkerChannelHandler interface {
	DispatchWork(context.Context, *connect.Request[pb.WorkerStream], *connect.ServerStream[pb.WorkItem]) error
	RecordArtifact(context.Context, *connect.Request[pb.Artifact]) (*connect.Response[emptypb.Empty], error)
	SubmitTelemetry(context.Context, *connect.ClientStream[pb.Span]) (*connect.Response[emptypb.Empty], error)
	ProposeForgeAction(context.Context, *connect.Request[pb.ForgeAction]) (*connect.Response[pb.ActionAck], error)
}

func NewWorkerChannelHandler(svc WorkerChannelHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	methods := pb.File_botfam_eventdelivery_v2_eventdelivery_proto.Services().ByName("WorkerChannel").Methods()
	dispatchWork := connect.NewServerStreamHandler(WorkerChannelDispatchWorkProcedure, svc.DispatchWork, connect.WithSchema(methods.ByName("DispatchWork")), connect.WithHandlerOptions(opts...))
	recordArtifact := connect.NewUnaryHandler(WorkerChannelRecordArtifactProcedure, svc.RecordArtifact, connect.WithSchema(methods.ByName("RecordArtifact")), connect.WithHandlerOptions(opts...))
	submitTelemetry := connect.NewClientStreamHandler(WorkerChannelSubmitTelemetryProcedure, svc.SubmitTelemetry, connect.WithSchema(methods.ByName("SubmitTelemetry")), connect.WithHandlerOptions(opts...))
	proposeForgeAction := connect.NewUnaryHandler(WorkerChannelProposeForgeActionProcedure, svc.ProposeForgeAction, connect.WithSchema(methods.ByName("ProposeForgeAction")), connect.WithHandlerOptions(opts...))
	return "/botfam.eventdelivery.v2.WorkerChannel/", route(map[string]http.Handler{
		WorkerChannelDispatchWorkProcedure:       dispatchWork,
		WorkerChannelRecordArtifactProcedure:     recordArtifact,
		WorkerChannelSubmitTelemetryProcedure:    submitTelemetry,
		WorkerChannelProposeForgeActionProcedure: proposeForgeAction,
	})
}

type ObservationPipelineHandler interface {
	Observe(context.Context, *connect.Request[pb.ObserveRequest], *connect.ServerStream[pb.RawObservation]) error
	Translate(context.Context, *connect.Request[pb.RawObservation], *connect.ServerStream[pb.WorkItem]) error
	RecordProcessed(context.Context, *connect.Request[pb.ProcessedMark]) (*connect.Response[emptypb.Empty], error)
}

func NewObservationPipelineHandler(svc ObservationPipelineHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	methods := pb.File_botfam_eventdelivery_v2_eventdelivery_proto.Services().ByName("ObservationPipeline").Methods()
	observe := connect.NewServerStreamHandler(ObservationPipelineObserveProcedure, svc.Observe, connect.WithSchema(methods.ByName("Observe")), connect.WithHandlerOptions(opts...))
	translate := connect.NewServerStreamHandler(ObservationPipelineTranslateProcedure, svc.Translate, connect.WithSchema(methods.ByName("Translate")), connect.WithHandlerOptions(opts...))
	recordProcessed := connect.NewUnaryHandler(ObservationPipelineRecordProcessedProcedure, svc.RecordProcessed, connect.WithSchema(methods.ByName("RecordProcessed")), connect.WithHandlerOptions(opts...))
	return "/botfam.eventdelivery.v2.ObservationPipeline/", route(map[string]http.Handler{
		ObservationPipelineObserveProcedure:         observe,
		ObservationPipelineTranslateProcedure:       translate,
		ObservationPipelineRecordProcessedProcedure: recordProcessed,
	})
}

type SessionStoreHandler interface {
	PutSessionDescriptor(context.Context, *connect.Request[pb.PutSessionDescriptorRequest]) (*connect.Response[emptypb.Empty], error)
	ListSessionDescriptors(context.Context, *connect.Request[pb.ListSessionDescriptorsRequest]) (*connect.Response[pb.ListSessionDescriptorsResponse], error)
}

func NewSessionStoreHandler(svc SessionStoreHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	methods := pb.File_botfam_eventdelivery_v2_eventdelivery_proto.Services().ByName("SessionStore").Methods()
	putSessionDescriptor := connect.NewUnaryHandler(SessionStorePutSessionDescriptorProcedure, svc.PutSessionDescriptor, connect.WithSchema(methods.ByName("PutSessionDescriptor")), connect.WithHandlerOptions(opts...))
	listSessionDescriptors := connect.NewUnaryHandler(SessionStoreListSessionDescriptorsProcedure, svc.ListSessionDescriptors, connect.WithSchema(methods.ByName("ListSessionDescriptors")), connect.WithHandlerOptions(opts...))
	return "/botfam.eventdelivery.v2.SessionStore/", route(map[string]http.Handler{
		SessionStorePutSessionDescriptorProcedure:   putSessionDescriptor,
		SessionStoreListSessionDescriptorsProcedure: listSessionDescriptors,
	})
}

func route(handlers map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler, ok := handlers[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})
}
