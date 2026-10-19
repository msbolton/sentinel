package grpcin

import (
	"context"

	"github.com/sentinel/ingest-service/internal/source/driver"
	ingestv1 "github.com/sentinel/proto-gen/sentinel/ingest/v1"
)

// service answers IngestService calls for one run of the driver.
type service struct {
	ingestv1.UnimplementedIngestServiceServer
	d    *Driver
	sink driver.Sink
	// run is the driver's run: when it ends, every call ends.
	run context.Context
	// calls is every open call, so a revoked token's can be ended.
	calls calls
}

func newService(d *Driver, sink driver.Sink, run context.Context) *service {
	return &service{d: d, sink: sink, run: run}
}
