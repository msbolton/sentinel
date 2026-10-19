package ingestv1_test

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

// The wire contract sub-projects 2 and 3 build against: the service name and
// its methods, and the Observation field numbers that must never move.
func TestIngestServiceContract(t *testing.T) {
	if got := ingestv1.IngestService_ServiceDesc.ServiceName; got != "sentinel.ingest.v1.IngestService" {
		t.Fatalf("service name = %q", got)
	}
	methods := map[string]bool{}
	for _, m := range ingestv1.IngestService_ServiceDesc.Methods {
		methods[m.MethodName] = true
	}
	for _, s := range ingestv1.IngestService_ServiceDesc.Streams {
		methods[s.StreamName] = true
		if s.StreamName == "StreamReports" && (!s.ClientStreams || !s.ServerStreams) {
			t.Errorf("StreamReports must stream both ways")
		}
	}
	for _, want := range []string{"GetSession", "ListEmplacements", "UpsertEmplacement", "StreamReports", "Publish"} {
		if !methods[want] {
			t.Errorf("missing rpc %s", want)
		}
	}

	fields := (&sentinelv1.Observation{}).ProtoReflect().Descriptor().Fields()
	for name, num := range map[protoreflect.Name]protoreflect.FieldNumber{"emplacement_id": 35, "polar": 36, "exercise": 37, "classification": 34} {
		f := fields.ByName(name)
		if f == nil || f.Number() != num {
			t.Errorf("Observation.%s: want field %d, got %v", name, num, f)
		}
	}
	if sentinelv1.IdentityType_IDENTITY_TYPE_EMPLACEMENT != 10 {
		t.Errorf("IDENTITY_TYPE_EMPLACEMENT = %d, want 10", sentinelv1.IdentityType_IDENTITY_TYPE_EMPLACEMENT)
	}
}

func TestReportNamesItsEmplacement(t *testing.T) {
	f := (&ingestv1.Report{}).ProtoReflect().Descriptor().Fields().ByName("emplacement_id")
	if f == nil || f.Number() != 4 || f.ContainingOneof() != nil {
		t.Fatalf("Report.emplacement_id must be field 4, outside the body oneof: %v", f)
	}
}
