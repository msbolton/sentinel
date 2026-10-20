package fusion

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/proto-gen/pbjson"
	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"

	"github.com/sentinel/entity-service/internal/events"
	"github.com/sentinel/entity-service/internal/store"
)

// EntityWriter applies fused state to entity rows.
type EntityWriter interface {
	ApplyFused(ctx context.Context, updates []store.FusedUpdate) ([]store.FusedResult, error)
	ApplyStatic(ctx context.Context, updates []store.StaticUpdate) ([]store.FusedResult, error)
	MarkExercise(ctx context.Context, ids []string) ([]store.EntityRecord, error)
}

// ObservationWriter persists every report.
type ObservationWriter interface {
	InsertObservations(ctx context.Context, rows []store.ObservationRow) error
}

// SourceWriter maintains per-feed provenance.
type SourceWriter interface {
	UpsertSources(ctx context.Context, rows []store.SourceRow) error
}

// EventSink publishes entity events.
type EventSink interface {
	Created(entity *sentinelv1.Entity, ref *sentinelv1.ObservationRef)
	Position(entity *sentinelv1.Entity, ref *sentinelv1.ObservationRef)
	Restored(entity *sentinelv1.Entity, previousState string)
	Updated(entity *sentinelv1.Entity, changed []string)
}

// Stats summarises one batch.
type Stats struct {
	Reports   int
	Accepted  int
	Created   int
	Rejected  map[string]int
	Restored  int
	Static    int
	EntityIDs int
}

// Processor fuses a batch of observations into entities.
type Processor struct {
	resolver     *Resolver
	entities     EntityWriter
	observations ObservationWriter
	sources      SourceWriter
	events       EventSink
	priority     Priority
	logger       *zap.Logger
}

// NewProcessor wires a processor.
func NewProcessor(resolver *Resolver, entities EntityWriter, observations ObservationWriter, sources SourceWriter, sink EventSink, priority Priority, logger *zap.Logger) *Processor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Processor{resolver: resolver, entities: entities, observations: observations, sources: sources, events: sink, priority: priority, logger: logger}
}

// ProcessBatch resolves, fuses, stores and announces one batch. It returns
// an error only for failures that should make the consumer retry the
// batch; a report that merely loses to a newer fix is stored as rejected.
func (p *Processor) ProcessBatch(ctx context.Context, observations []*sentinelv1.Observation) (Stats, error) {
	stats := Stats{Reports: len(observations), Rejected: map[string]int{}}
	if len(observations) == 0 {
		return stats, nil
	}
	reports := make([]*Report, 0, len(observations))
	for _, o := range observations {
		if o == nil {
			continue
		}
		reports = append(reports, Prepare(o, p.priority))
	}
	Dedupe(reports)

	res, err := p.resolver.Resolve(ctx, reports)
	if err != nil {
		return stats, err
	}
	stats.Created = len(res.Created)
	stats.EntityIDs = len(res.IDs)

	// Candidates: the batch winner per key (valid) and static reports.
	var fused []store.FusedUpdate
	var static []store.StaticUpdate
	byEntity := map[string]*Report{}
	for _, r := range reports {
		id := res.IDs[r.Key]
		if id == "" {
			r.Valid = false
			r.Reject = "UNRESOLVED"
			continue
		}
		switch {
		case r.Valid:
			fused = append(fused, fusedUpdate(id, r))
			byEntity[id] = r
		case r.Reject == RejectNoPosition:
			static = append(static, staticUpdate(id, r))
			if _, taken := byEntity[id]; !taken {
				byEntity[id] = r
			}
		}
	}

	applied := map[string]store.FusedResult{}
	if len(fused) > 0 {
		results, err := p.entities.ApplyFused(ctx, fused)
		if err != nil {
			return stats, err
		}
		for _, r := range results {
			applied[r.EntityID] = r
		}
	}
	staticApplied := map[string]store.FusedResult{}
	if len(static) > 0 {
		results, err := p.entities.ApplyStatic(ctx, static)
		if err != nil {
			return stats, err
		}
		for _, r := range results {
			staticApplied[r.EntityID] = r
		}
	}

	// Mark the winners that lost to a newer fix already on the entity.
	for _, r := range reports {
		if !r.Valid {
			continue
		}
		if _, ok := applied[res.IDs[r.Key]]; !ok {
			r.Valid = false
			r.Reject = RejectStaleFix
		}
	}

	// An exercise report marks its entity whatever became of its fix: the
	// upserts carry the mark only for the winner, and a duplicate or stale
	// exercise report would otherwise leave the entity looking live.
	marked, err := p.markExercise(ctx, reports, res.IDs, applied, staticApplied)
	if err != nil {
		return stats, err
	}

	// Persist every report and the per-feed provenance.
	rows := make([]store.ObservationRow, 0, len(reports))
	srcRows := make([]store.SourceRow, 0, len(reports))
	for _, r := range reports {
		id := res.IDs[r.Key]
		if id == "" {
			stats.Rejected[r.Reject]++
			continue
		}
		rows = append(rows, observationRow(id, r))
		srcRows = append(srcRows, store.SourceRow{EntityID: id, FeedID: r.Obs.FeedId, Source: pbjson.EnumName(r.Obs.Source), SourceEntityID: r.Obs.SourceEntityId, ObservedAt: r.ObservedAt, ReceivedAt: r.ReceivedAt, Count: 1})
		if r.Valid {
			stats.Accepted++
		} else {
			stats.Rejected[r.Reject]++
		}
	}
	if err := p.observations.InsertObservations(ctx, rows); err != nil {
		return stats, fmt.Errorf("storing observations: %w", err)
	}
	if err := p.sources.UpsertSources(ctx, srcRows); err != nil {
		return stats, fmt.Errorf("storing sources: %w", err)
	}

	// Announce, in report order: new entities, restorations, positions.
	announced := map[string]bool{}
	for _, r := range reports {
		id := res.IDs[r.Key]
		if id == "" || announced[id] {
			continue
		}
		result, ok := applied[id]
		isStatic := false
		if !ok {
			result, ok = staticApplied[id]
			isStatic = true
		}
		if !ok || byEntity[id] != r {
			continue
		}
		announced[id] = true
		entity := events.EntityProto(result.Record, identitiesProto(r), []*sentinelv1.SourceContribution{contribution(r)})
		ref := observationRef(r)
		// A feed raised the entity's classification: say so explicitly, so
		// consumers that apply a ceiling see the crossing, not just a fix.
		raised := result.PreviousClassification != "" && result.Record.Classification != result.PreviousClassification
		switch {
		case res.Created[r.Key]:
			p.events.Created(entity, ref)
		case isStatic:
			changed := []string{"platforms"}
			if raised {
				changed = append(changed, "classification")
			}
			p.events.Updated(entity, changed)
			stats.Static++
		default:
			if result.PreviousState == "STALE" || result.PreviousState == "AGED_OUT" {
				p.events.Restored(entity, result.PreviousState)
				stats.Restored++
			}
			if raised {
				p.events.Updated(entity, []string{"classification"})
			}
			p.events.Position(entity, ref)
		}
	}
	// Entities marked by a report that did not carry the mark itself.
	for _, rec := range marked {
		p.events.Updated(events.EntityProto(&rec, nil, nil), []string{"exercise"})
	}
	return stats, nil
}

// markExercise marks the entities of exercise reports whose upsert did not
// already carry the mark. It patches the applied results so the events of
// this batch show the mark, and returns the rows it changed.
func (p *Processor) markExercise(ctx context.Context, reports []*Report, ids map[Key]string, applied, staticApplied map[string]store.FusedResult) ([]store.EntityRecord, error) {
	// ApplyStatic runs after ApplyFused, so a mark its row carries may be
	// newer than the fused result's snapshot of the same entity.
	for id, s := range staticApplied {
		if a, ok := applied[id]; ok && s.Record.Exercise {
			a.Record.Exercise = true
		}
	}
	var todo []string
	seen := map[string]bool{}
	for _, r := range reports {
		id := ids[r.Key]
		if id == "" || seen[id] || !r.Obs.GetExercise() {
			continue
		}
		seen[id] = true
		if a, ok := applied[id]; ok && a.Record.Exercise {
			continue
		}
		if a, ok := staticApplied[id]; ok && a.Record.Exercise {
			continue
		}
		todo = append(todo, id)
	}
	recs, err := p.entities.MarkExercise(ctx, todo)
	if err != nil {
		return nil, fmt.Errorf("marking exercise entities: %w", err)
	}
	for _, rec := range recs {
		if a, ok := applied[rec.ID]; ok {
			a.Record.Exercise = true
		}
		if a, ok := staticApplied[rec.ID]; ok {
			a.Record.Exercise = true
		}
	}
	return recs, nil
}

func fusedUpdate(id string, r *Report) store.FusedUpdate {
	o := r.Obs
	u := store.FusedUpdate{
		EntityID: id, ObservedAt: r.ObservedAt, FeedID: o.FeedId, Source: pbjson.EnumName(o.Source), SourceEntityID: o.SourceEntityId,
		KeyType: r.Key.Type, KeyValue: r.Key.Value, Name: o.DisplayName,
		Lat: o.Position.Latitude, Lon: o.Position.Longitude, Altitude: o.Position.AltitudeMeters,
		Heading: o.Heading, SpeedKnots: o.SpeedKnots, Course: o.Course,
		TrackEnvironment: enumOrEmpty(o.TrackEnvironment), CountryOfOrigin: o.CountryOfOrigin,
		Platform: platformJSON(o), Kinematics: kinematicsJSON(o),
		Classification: enumOrEmpty(o.Classification), Exercise: o.GetExercise(),
	}
	if o.Uncertainty != nil && o.Uncertainty.CircularError != 0 {
		ce := o.Uncertainty.CircularError
		u.CircularError = &ce
	}
	return u
}

func staticUpdate(id string, r *Report) store.StaticUpdate {
	o := r.Obs
	return store.StaticUpdate{EntityID: id, FeedID: o.FeedId, Source: pbjson.EnumName(o.Source), SourceEntityID: o.SourceEntityId, Name: o.DisplayName, CountryOfOrigin: o.CountryOfOrigin, Platform: platformJSON(o), Classification: enumOrEmpty(o.Classification), Exercise: o.GetExercise()}
}

func observationRow(id string, r *Report) store.ObservationRow {
	o := r.Obs
	row := store.ObservationRow{
		ObservationID: o.ObservationId, ObservedAt: r.ObservedAt, ReceivedAt: r.ReceivedAt, EntityID: id,
		FeedID: o.FeedId, Driver: o.Driver, Decoder: o.Decoder, SourceEntityID: o.SourceEntityId, Source: pbjson.EnumName(o.Source), SensorID: o.SensorId,
		Accepted: r.Valid, RejectReason: r.Reject, EntityType: enumOrEmpty(o.EntityType),
		Heading: o.Heading, SpeedKnots: o.SpeedKnots, Course: o.Course,
		Identities: identitiesJSON(r), Platform: platformJSON(o),
	}
	if o.Position != nil {
		row.HasPosition = true
		row.Lat, row.Lon = o.Position.Latitude, o.Position.Longitude
		row.Altitude = o.Position.AltitudeMeters
	}
	if v := o.Velocity; v != nil {
		row.Velocity = &[3]float64{v.North, v.East, v.Up}
	}
	if a := o.Acceleration; a != nil {
		row.Acceleration = &[3]float64{a.North, a.East, a.Up}
	}
	if u := o.Uncertainty; u != nil {
		if u.CircularError != 0 {
			ce := u.CircularError
			row.CircularError = &ce
		}
		if u.AltitudeError != 0 {
			ae := u.AltitudeError
			row.AltitudeError = &ae
		}
	}
	if o.Raw != nil {
		row.RawContentType = o.Raw.ContentType
		row.Raw = o.Raw.Data
		row.RawTruncated = o.Raw.Truncated
	}
	return row
}

func observationRef(r *Report) *sentinelv1.ObservationRef {
	o := r.Obs
	return &sentinelv1.ObservationRef{ObservationId: o.ObservationId, FeedId: o.FeedId, Source: o.Source, SourceEntityId: o.SourceEntityId, SourceTimestamp: timestamppb.New(r.ObservedAt)}
}

func contribution(r *Report) *sentinelv1.SourceContribution {
	o := r.Obs
	return &sentinelv1.SourceContribution{FeedId: o.FeedId, Source: o.Source, SourceEntityId: o.SourceEntityId, LastObservedAt: timestamppb.New(r.ObservedAt), LastReceivedAt: timestamppb.New(r.ReceivedAt)}
}

func identitiesProto(r *Report) []*sentinelv1.Identity {
	out := make([]*sentinelv1.Identity, 0, len(r.Identities))
	for _, k := range r.Identities {
		t, _ := pbjson.ParseEnum[sentinelv1.IdentityType](k.Type)
		out = append(out, &sentinelv1.Identity{Type: t, Value: k.Value})
	}
	if len(out) == 0 {
		t, _ := pbjson.ParseEnum[sentinelv1.IdentityType](r.Key.Type)
		out = append(out, &sentinelv1.Identity{Type: t, Value: r.Key.Value})
	}
	return out
}

func identitiesJSON(r *Report) json.RawMessage {
	type id struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	list := make([]id, 0, len(r.Identities))
	for _, k := range r.Identities {
		list = append(list, id{k.Type, k.Value})
	}
	b, _ := json.Marshal(list)
	return b
}

// platformJSON renders the detail block as {"<kind>": {...}}, the shape
// entities.platformData keeps.
func platformJSON(o *sentinelv1.Observation) json.RawMessage {
	if o.Platform == nil || o.Platform.Detail == nil {
		return nil
	}
	b, err := pbjson.Marshal(o.Platform)
	if err != nil {
		return nil
	}
	return b
}

func kinematicsJSON(o *sentinelv1.Observation) json.RawMessage {
	if o.Velocity == nil && o.Acceleration == nil {
		return nil
	}
	k := map[string]interface{}{}
	if v := o.Velocity; v != nil {
		k["velocity"] = map[string]float64{"north": v.North, "east": v.East, "up": v.Up}
	}
	if a := o.Acceleration; a != nil {
		k["acceleration"] = map[string]float64{"north": a.North, "east": a.East, "up": a.Up}
	}
	b, _ := json.Marshal(k)
	return b
}

// enumOrEmpty renders an enum's bare name, or "" for the zero value.
func enumOrEmpty(e protoreflect.Enum) string {
	if e.Number() == 0 {
		return ""
	}
	return pbjson.EnumName(e)
}
