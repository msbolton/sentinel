package fusion

import (
	"context"
	"fmt"
	"testing"
	"time"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sentinel/entity-service/internal/identity"
)

type memIdentities struct {
	ids      map[Key]string
	next     int
	lookups  int
	creates  int
	bindings []identity.Binding
}

func newMemIdentities() *memIdentities { return &memIdentities{ids: map[Key]string{}} }

func (m *memIdentities) Lookup(_ context.Context, keys []Key) (map[Key]string, error) {
	m.lookups++
	out := map[Key]string{}
	for _, k := range keys {
		if id, ok := m.ids[k]; ok {
			out[k] = id
		}
	}
	return out, nil
}

func (m *memIdentities) CreateEntities(_ context.Context, specs []identity.NewEntity) (map[Key]string, map[Key]bool, error) {
	m.creates++
	ids, created := map[Key]string{}, map[Key]bool{}
	for _, s := range specs {
		if id, ok := m.ids[s.Key]; ok { // lost a race
			ids[s.Key] = id
			continue
		}
		m.next++
		id := fmt.Sprintf("e-%d", m.next)
		m.ids[s.Key] = id
		ids[s.Key] = id
		created[s.Key] = true
	}
	return ids, created, nil
}

func (m *memIdentities) AttachIdentities(_ context.Context, b []identity.Binding) (int, error) {
	m.bindings = append(m.bindings, b...)
	conflicts := 0
	for _, x := range b {
		if existing, ok := m.ids[x.Key]; ok && existing != x.EntityID {
			conflicts++
			continue
		}
		m.ids[x.Key] = x.EntityID
	}
	return conflicts, nil
}

func TestResolveCreatesOncePerKeyAndCaches(t *testing.T) {
	store := newMemIdentities()
	r := NewResolver(store, 100, time.Hour)
	a := Prepare(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0, 1, 1), nil)
	b := Prepare(obs("adsb-lol", sentinelv1.EntitySource_ENTITY_SOURCE_ADSB_LOL, "A1B2C3", t0, 1, 1), nil)
	c := Prepare(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "FFFFFF", t0, 1, 1), nil)

	res, err := r.Resolve(context.Background(), []*Report{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	if res.IDs[a.Key] == "" || res.IDs[a.Key] != res.IDs[b.Key] || res.IDs[c.Key] == res.IDs[a.Key] {
		t.Fatalf("two feeds, one aircraft must share an entity: %v", res.IDs)
	}
	if len(res.Created) != 2 || store.creates != 1 || store.lookups != 1 {
		t.Fatalf("created=%v creates=%d lookups=%d", res.Created, store.creates, store.lookups)
	}
	if len(store.bindings) == 0 || store.bindings[0].Key.Type != "CALLSIGN" {
		t.Errorf("callsign must be attached as a secondary identity: %v", store.bindings)
	}

	// Second batch: cache hit, no store calls for known keys.
	res2, _ := r.Resolve(context.Background(), []*Report{a})
	if store.lookups != 1 || res2.IDs[a.Key] != res.IDs[a.Key] || len(res2.Created) != 0 {
		t.Fatalf("cache miss: lookups=%d", store.lookups)
	}

	r.Forget(a.Key)
	_, _ = r.Resolve(context.Background(), []*Report{a})
	if store.lookups != 2 {
		t.Fatalf("forget must force a lookup, lookups=%d", store.lookups)
	}
}

func TestResolveFeedLocalKeysStaySeparate(t *testing.T) {
	store := newMemIdentities()
	r := NewResolver(store, 100, time.Hour)
	a := Prepare(&sentinelv1.Observation{FeedId: "mqtt-a", SourceEntityId: "trk-1", Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 1}}, nil)
	b := Prepare(&sentinelv1.Observation{FeedId: "mqtt-b", SourceEntityId: "trk-1", Position: &sentinelv1.Coordinate{Latitude: 1, Longitude: 1}}, nil)
	res, _ := r.Resolve(context.Background(), []*Report{a, b})
	if res.IDs[a.Key] == res.IDs[b.Key] {
		t.Fatal("the same source id on two feeds must not fuse")
	}
}

func TestResolveLostRaceUsesWinner(t *testing.T) {
	store := newMemIdentities()
	r := NewResolver(store, 100, time.Hour)
	a := Prepare(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "A1B2C3", t0, 1, 1), nil)
	store.ids[a.Key] = "e-other" // registered between Lookup and Create by someone else: simulate by pre-seeding after lookup is impossible here, so pre-seed and check no creation
	res, _ := r.Resolve(context.Background(), []*Report{a})
	if res.IDs[a.Key] != "e-other" || len(res.Created) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

// The built-in emplacements feed and the gRPC connection that reports a
// mobile emplacement's own position both name it by EMPLACEMENT: one entity.
func TestResolveEmplacementFromTwoFeedsIsOneEntity(t *testing.T) {
	store := newMemIdentities()
	r := NewResolver(store, 100, time.Hour)
	mk := func(feed string) *Report {
		return Prepare(&sentinelv1.Observation{FeedId: feed, SourceEntityId: "e-1", SourceTimestamp: timestamppb.New(t0),
			Identities: []*sentinelv1.Identity{{Type: sentinelv1.IdentityType_IDENTITY_TYPE_EMPLACEMENT, Value: "e-1"}},
			Position:   &sentinelv1.Coordinate{Latitude: 1, Longitude: 1}}, nil)
	}
	a, b := mk("emplacements"), mk("grpc-1")
	res, err := r.Resolve(context.Background(), []*Report{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if res.IDs[a.Key] == "" || res.IDs[a.Key] != res.IDs[b.Key] || len(res.Created) != 1 {
		t.Fatalf("ids=%v created=%v", res.IDs, res.Created)
	}
}

func TestInvalidateDropsCachedKeys(t *testing.T) {
	mem := newMemIdentities()
	r := NewResolver(mem, 100, time.Hour)
	rep := Prepare(obs("opensky", sentinelv1.EntitySource_ENTITY_SOURCE_OPENSKY, "ABC123", t0, 1, 1), nil)
	if _, err := r.Resolve(context.Background(), []*Report{rep}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background(), []*Report{rep}); err != nil {
		t.Fatal(err)
	}
	if mem.lookups != 1 {
		t.Fatalf("lookups before invalidate = %d, want 1 (cached)", mem.lookups)
	}
	r.Invalidate()
	if _, err := r.Resolve(context.Background(), []*Report{rep}); err != nil {
		t.Fatal(err)
	}
	if mem.lookups != 2 {
		t.Fatalf("lookups after invalidate = %d, want 2", mem.lookups)
	}
}
