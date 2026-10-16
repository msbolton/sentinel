// Package consumer keeps the OpenSearch index in step with entities.events.
package consumer

import (
	"context"
	"strconv"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/sentinel/go-common/kafka"
	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/search-service/internal/opensearch"
)

// Indexer is what the consumer needs from the OpenSearch client.
type Indexer interface {
	IndexEntity(ctx context.Context, doc opensearch.EntityDocument) error
	DeleteEntity(ctx context.Context, id string)
	UpdateFields(ctx context.Context, id string, fields map[string]interface{}) error
}

// EntityIndexer consumes entity events and indexes them.
type EntityIndexer struct {
	indexer Indexer
	bc      *kafka.BatchConsumer
	logger  *zap.Logger
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewEntityIndexer creates and subscribes the consumer.
func NewEntityIndexer(brokers string, indexer Indexer, logger *zap.Logger) (*EntityIndexer, error) {
	bc, err := kafka.NewBatchConsumer(brokers, kafka.GroupSearchService, "search-service", "earliest",
		kafka.BatchOptions{MaxMessages: 500, MaxWait: time.Second}, logger)
	if err != nil {
		return nil, err
	}
	if err := bc.Subscribe([]string{kafka.TopicEntityEvents}); err != nil {
		_ = bc.Close()
		return nil, err
	}
	return &EntityIndexer{indexer: indexer, bc: bc, logger: logger, done: make(chan struct{})}, nil
}

// Start begins consuming in the background.
func (e *EntityIndexer) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	go func() {
		defer close(e.done)
		e.logger.Info("entity indexer started", zap.String("topic", kafka.TopicEntityEvents))
		if err := e.bc.Run(ctx, e.handle); err != nil {
			e.logger.Error("entity indexer stopped", zap.Error(err))
		}
	}()
}

// Stop ends consumption.
func (e *EntityIndexer) Stop() {
	if e.cancel != nil {
		e.cancel()
		<-e.done
	}
	_ = e.bc.Close()
}

// op is one index operation derived from an event.
type op struct {
	kind   string
	id     string
	doc    *opensearch.EntityDocument
	fields map[string]interface{}
}

// handle coalesces a batch: one index call per entity for the newest
// snapshot, deletes and state updates applied in order after it.
func (e *EntityIndexer) handle(ctx context.Context, msgs []*ckafka.Message) error {
	var ops []op
	latest := map[string]int{}
	for _, m := range msgs {
		var ev sentinelv1.EntityEvent
		if err := proto.Unmarshal(m.Value, &ev); err != nil {
			e.logger.Warn("undecodable entity event skipped", zap.Error(err))
			continue
		}
		switch x := ev.Event.(type) {
		case *sentinelv1.EntityEvent_Created:
			ops = e.coalesce(ops, latest, DocumentFromEntity(x.Created.Entity))
		case *sentinelv1.EntityEvent_Updated:
			ops = e.coalesce(ops, latest, DocumentFromEntity(x.Updated.Entity))
		case *sentinelv1.EntityEvent_Position:
			ops = e.coalesce(ops, latest, DocumentFromEntity(x.Position.Entity))
		case *sentinelv1.EntityEvent_Restored:
			ops = e.coalesce(ops, latest, DocumentFromEntity(x.Restored.Entity))
		case *sentinelv1.EntityEvent_Deleted:
			if x.Deleted.Bulk {
				e.logger.Warn("bulk delete event received; index not cleared automatically")
				continue
			}
			ops = append(ops, op{kind: "delete", id: ev.EntityId})
		case *sentinelv1.EntityEvent_Stale:
			ops = append(ops, op{kind: "update", id: ev.EntityId, fields: map[string]interface{}{"ageoutState": "STALE"}})
		case *sentinelv1.EntityEvent_AgedOut:
			ops = append(ops, op{kind: "update", id: ev.EntityId, fields: map[string]interface{}{"ageoutState": "AGED_OUT"}})
		}
	}
	for _, o := range ops {
		switch o.kind {
		case "index":
			if o.doc == nil {
				continue
			}
			if err := e.indexer.IndexEntity(ctx, *o.doc); err != nil {
				e.logger.Error("failed to index entity", zap.String("id", o.doc.ID), zap.Error(err))
			}
		case "delete":
			e.indexer.DeleteEntity(ctx, o.id)
		case "update":
			if err := e.indexer.UpdateFields(ctx, o.id, o.fields); err != nil {
				e.logger.Debug("ageout update skipped", zap.String("id", o.id), zap.Error(err))
			}
		}
	}
	return nil
}

// coalesce keeps only the newest snapshot per entity within the batch.
func (e *EntityIndexer) coalesce(ops []op, latest map[string]int, doc *opensearch.EntityDocument) []op {
	if doc == nil {
		return ops
	}
	if i, ok := latest[doc.ID]; ok {
		ops[i].doc = nil // superseded; the newest snapshot wins
	}
	latest[doc.ID] = len(ops)
	return append(ops, op{kind: "index", id: doc.ID, doc: doc})
}

// DocumentFromEntity maps the wire snapshot to the index document.
func DocumentFromEntity(en *sentinelv1.Entity) *opensearch.EntityDocument {
	if en == nil || en.Id == "" {
		return nil
	}
	doc := &opensearch.EntityDocument{ID: en.Id, Name: en.Name, EntityType: pbjson.EnumName(en.EntityType)}
	set := func(dst **string, v string) {
		if v != "" {
			s := v
			*dst = &s
		}
	}
	set(&doc.Description, en.Description)
	set(&doc.Source, enumOrEmpty(en.Source))
	set(&doc.Classification, enumOrEmpty(en.Classification))
	set(&doc.Affiliation, enumOrEmpty(en.Affiliation))
	set(&doc.TrackEnvironment, enumOrEmpty(en.TrackEnvironment))
	set(&doc.OperationalStatus, enumOrEmpty(en.OperationalStatus))
	set(&doc.CountryOfOrigin, en.CountryOfOrigin)
	set(&doc.SourceEntityID, en.SourceEntityId)
	set(&doc.FeedID, en.FeedId)
	set(&doc.AgeoutState, enumOrEmpty(en.AgeoutState))
	if en.Position != nil {
		doc.Position = &opensearch.GeoPoint{Lat: en.Position.Latitude, Lon: en.Position.Longitude}
	}
	if len(en.Affiliations) > 0 {
		doc.Affiliations = en.Affiliations
	}
	if len(en.Metadata) > 0 {
		doc.Metadata = map[string]interface{}{}
		for k, v := range en.Metadata {
			doc.Metadata[k] = v
		}
	}
	if en.CreatedAt != nil {
		set(&doc.CreatedAt, en.CreatedAt.AsTime().UTC().Format(time.RFC3339Nano))
	}
	if en.UpdatedAt != nil {
		set(&doc.UpdatedAt, en.UpdatedAt.AsTime().UTC().Format(time.RFC3339Nano))
	}
	if en.LastSeenAt != nil {
		set(&doc.LastSeenAt, en.LastSeenAt.AsTime().UTC().Format(time.RFC3339Nano))
	}
	for _, id := range en.Identities {
		if id != nil && id.Value != "" {
			doc.Identities = append(doc.Identities, pbjson.EnumName(id.Type)+":"+id.Value)
		}
	}
	doc.PlatformIdentifiers = platformIdentifiers(en)
	return doc
}

func platformIdentifiers(en *sentinelv1.Entity) *opensearch.PlatformIdentifiers {
	p := &opensearch.PlatformIdentifiers{}
	any := false
	set := func(dst **string, v string) {
		if v != "" {
			s := v
			*dst = &s
			any = true
		}
	}
	for _, d := range en.Platforms {
		switch x := d.GetDetail().(type) {
		case *sentinelv1.PlatformDetail_Adsb:
			set(&p.ICAOHex, x.Adsb.IcaoHex)
			set(&p.Registration, x.Adsb.Registration)
			set(&p.Callsign, x.Adsb.AircraftId)
			set(&p.Squawk, x.Adsb.Squawk)
		case *sentinelv1.PlatformDetail_Ais:
			set(&p.MMSI, x.Ais.Mmsi)
			set(&p.IMO, x.Ais.Imo)
			set(&p.Callsign, x.Ais.Callsign)
		case *sentinelv1.PlatformDetail_Tle:
			if x.Tle.NoradId != 0 {
				n := int(x.Tle.NoradId)
				p.NoradID = &n
				any = true
			}
		}
	}
	for _, id := range en.Identities {
		if id == nil {
			continue
		}
		switch pbjson.EnumName(id.Type) {
		case "ICAO24":
			if p.ICAOHex == nil {
				set(&p.ICAOHex, id.Value)
			}
		case "MMSI":
			if p.MMSI == nil {
				set(&p.MMSI, id.Value)
			}
		case "NORAD_ID":
			if p.NoradID == nil {
				if n, err := strconv.Atoi(id.Value); err == nil {
					p.NoradID = &n
					any = true
				}
			}
		case "CALLSIGN":
			if p.Callsign == nil {
				set(&p.Callsign, id.Value)
			}
		}
	}
	if !any {
		return nil
	}
	return p
}

func enumOrEmpty(e protoreflect.Enum) string {
	if e.Number() == 0 {
		return ""
	}
	return pbjson.EnumName(e)
}
