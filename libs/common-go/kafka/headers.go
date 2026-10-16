package kafka

import "google.golang.org/protobuf/proto"

// Header keys every protobuf message on the bus carries, plus the two
// topic-specific ones. Consumers can route on these without decoding the
// value.
const (
	HeaderContentType   = "content-type"
	HeaderSchema        = "schema"         // fully qualified message name, e.g. sentinel.v1.Observation
	HeaderSchemaVersion = "schema-version" // bumped only for a breaking change, which also gets a new topic
	HeaderProducer      = "producer"       // service name
	HeaderFeedID        = "feed-id"        // observations only
	HeaderEventType     = "event-type"     // entity events only: the EntityEvent oneof case name
	HeaderError         = "error"          // dead-letter topic only

	ContentTypeProtobuf = "application/x-protobuf"
	SchemaVersion       = "1"
)

// ProtoHeaders returns the standard headers for msg produced by producer,
// merged with extra (which wins on conflict).
func ProtoHeaders(msg proto.Message, producer string, extra map[string]string) map[string]string {
	h := map[string]string{
		HeaderContentType:   ContentTypeProtobuf,
		HeaderSchema:        string(msg.ProtoReflect().Descriptor().FullName()),
		HeaderSchemaVersion: SchemaVersion,
		HeaderProducer:      producer,
	}
	for k, v := range extra {
		h[k] = v
	}
	return h
}
