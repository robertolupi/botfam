package contract_test

import (
	"testing"

	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestProtoContractSurface(t *testing.T) {
	services := pb.File_botfam_eventdelivery_v2_eventdelivery_proto.Services()
	for _, name := range []string{
		"SessionResolver",
		"Lease",
		"WorkerChannel",
		"ObservationPipeline",
		"SessionStore",
	} {
		if services.ByName(protoreflect.Name(name)) == nil {
			t.Fatalf("service %s missing", name)
		}
	}
	if services.ByName("BotfamNotifications") != nil {
		t.Fatal("agent-facing notification service must not exist in M0b")
	}

	forgeAction := pb.File_botfam_eventdelivery_v2_eventdelivery_proto.Messages().ByName("ForgeAction")
	for _, field := range []string{"work_item_id", "action_key", "tool_name", "arguments_json"} {
		if forgeAction.Fields().ByName(protoreflect.Name(field)) == nil {
			t.Fatalf("ForgeAction.%s missing", field)
		}
	}

	lease := services.ByName(protoreflect.Name("Lease"))
	for _, method := range []string{"Acquire", "Renew", "Release"} {
		if lease.Methods().ByName(protoreflect.Name(method)) == nil {
			t.Fatalf("Lease.%s missing", method)
		}
	}
}
