package terrain

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// ErrUnavailable means there is no height for a point right now: the tile
// could not be fetched or decoded in time, the tile list is unknown, or the
// point is land that GLO-30 does not cover. It is never returned for sea.
var ErrUnavailable = errors.New("terrain unavailable")

// failureTTL is how long a failed fetch is remembered before trying again.
const failureTTL = 30 * time.Second

// Config sets up a Service backed by Copernicus GLO-30.
type Config struct {
	SourceURL     string        // default "https://copernicus-dem-30m.s3.amazonaws.com"
	CacheDir      string        // required for New
	MaxCacheBytes int64         // default 2 GiB
	FetchTimeout  time.Duration // default 300s
	// JoinWindow is how long a fetch already under way, for a tile or the
	// tile list, takes on more waiting callers (default 2s). A height
	// lookup that finds an older fetch fails at once with ErrUnavailable
	// rather than waiting on it, and the fetch carries on to FetchTimeout.
	// Set it to the callers' own wait, so a hung fetch holds a caller that
	// looks up heights one at a time for about one window, not one wait
	// per lookup.
	JoinWindow time.Duration
	MemTiles   int // decoded tiles kept in memory, default 4
	HTTPClient *http.Client
	Logger     *zap.Logger
}

// Stats counts the service's work. Hits and Misses count height lookups on
// listed tiles answered from, or not from, the decoded tiles in memory.
// Downloads and Failures count tile fetches; CacheBytes is the size of the
// tiles in the cache directory.
type Stats struct{ Hits, Misses, Downloads, Failures, CacheBytes int64 }

// Service answers ground heights. It is safe for concurrent use.
type Service struct {
	cfg    Config
	static func(lat, lon float64) float64
	log    *zap.Logger
	client *http.Client
	now    func() time.Time

	hits, misses, downloads, failures atomic.Int64
	warnOnce                          sync.Once

	mu        sync.Mutex
	mem       *memLRU
	disk      map[tileID]*diskEntry
	diskBytes int64
	diskOK    bool

	index        map[tileID]struct{}
	indexLoading *flight[map[tileID]struct{}]
	indexFailed  time.Time

	loading map[tileID]*flight[*raster]
	failed  map[tileID]time.Time
}

// flight is one piece of work that concurrent callers share. Its result
// fields are written before done is closed and read only after.
type flight[T any] struct {
	done    chan struct{}
	started time.Time
	val     T
	err     error
}

func newFlight[T any](now time.Time) *flight[T] {
	return &flight[T]{done: make(chan struct{}), started: now}
}

// closed reports whether a caller who did not start f should not wait on
// it: f has been running for the join window or longer. A caller that
// started f, or one that must have the result (Preload), is never turned
// away.
func (f *flight[T]) closed(now time.Time, window time.Duration) bool {
	return now.Sub(f.started) >= window
}

// errStillLoading is what a caller turned away from an old flight gets.
func errStillLoading(what string, age time.Duration) error {
	return fmt.Errorf("terrain: %s still loading after %v; not waiting for it", what, age.Round(time.Millisecond))
}

// wait blocks until the flight lands or ctx ends. The work carries on
// either way.
func (f *flight[T]) wait(ctx context.Context) (T, error) {
	select {
	case <-f.done:
		return f.val, f.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// New returns a Service that fetches Copernicus GLO-30 tiles on demand and
// caches them in cfg.CacheDir. If the directory cannot be written, the
// service still works, keeping tiles in memory only.
func New(cfg Config) (*Service, error) {
	if cfg.CacheDir == "" {
		return nil, errors.New("terrain: CacheDir is required")
	}
	if cfg.SourceURL == "" {
		cfg.SourceURL = DefaultSourceURL
	}
	cfg.SourceURL = strings.TrimRight(cfg.SourceURL, "/")
	if cfg.MaxCacheBytes <= 0 {
		cfg.MaxCacheBytes = 2 << 30
	}
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = 300 * time.Second
	}
	if cfg.JoinWindow <= 0 {
		cfg.JoinWindow = 2 * time.Second
	}
	if cfg.MemTiles <= 0 {
		cfg.MemTiles = 4
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	s := &Service{
		cfg:     cfg,
		log:     cfg.Logger,
		client:  cfg.HTTPClient,
		now:     time.Now,
		mem:     newMemLRU(cfg.MemTiles),
		disk:    map[tileID]*diskEntry{},
		diskOK:  true,
		loading: map[tileID]*flight[*raster]{},
		failed:  map[tileID]time.Time{},
	}
	s.openCacheDir()
	return s, nil
}

// NewStatic returns a Service whose heights come from a function, already
// ellipsoidal, with no network or disk. It is for tests and simulations.
func NewStatic(hae func(lat, lon float64) float64) *Service {
	return &Service{static: hae, now: time.Now}
}

func unavailable(err error) error { return fmt.Errorf("%w: %w", ErrUnavailable, err) }

// HeightHAE returns the ground height in metres above the WGS84 ellipsoid.
// Sea, meaning any tile GLO-30 does not list outside a known gap, is 0 m
// above mean sea level. Waiting for a tile honours ctx; a download the
// caller gives up on carries on and is cached for the next call. A caller
// joins a download another caller started only within Config.JoinWindow of
// its start; after that it gets ErrUnavailable at once until the download
// lands.
func (s *Service) HeightHAE(ctx context.Context, lat, lon float64) (float64, error) {
	if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lon, 0) || lat < -90 || lat > 90 {
		return 0, fmt.Errorf("terrain: no height for position (%g, %g)", lat, lon)
	}
	if s.static != nil {
		return s.static(lat, lon), nil
	}
	id := tileOf(lat, lon)

	s.mu.Lock()
	r := s.memGetLocked(id)
	s.mu.Unlock()
	if r != nil {
		s.hits.Add(1)
		return MSLToHAE(lat, lon, s.interpolate(ctx, id, r, lat, lon)), nil
	}

	idx, err := s.tileIndex(ctx, false)
	if err != nil {
		return 0, unavailable(err)
	}
	if _, listed := idx[id]; !listed {
		if inKnownGap(id) {
			return 0, fmt.Errorf("%w: %s is in a known gap in GLO-30", ErrUnavailable, id.key())
		}
		return MSLToHAE(lat, lon, 0), nil
	}

	s.misses.Add(1)
	r, err = s.tile(ctx, id, false)
	if err != nil {
		return 0, unavailable(err)
	}
	return MSLToHAE(lat, lon, s.interpolate(ctx, id, r, lat, lon)), nil
}

// interpolate reads a height from tile id, consulting neighbouring tiles only
// for a point in the strip past the tile's outermost pixel centres. A
// neighbour that is unlisted is sea. A listed neighbour is used only if it is
// already in memory or on disk; otherwise its download is started in the
// background and the tile's own edge value is used for now, as it is for a
// neighbour in a known gap or one that failed.
func (s *Service) interpolate(ctx context.Context, id tileID, r *raster, lat, lon float64) float64 {
	if r.interior(lat, lon) {
		return heightAt(id, r, lat, lon, noNeighbours)
	}
	got := map[tileID]*raster{id: r}
	return heightAt(id, r, lat, lon, func(nid tileID) *raster {
		if v, ok := got[nid]; ok {
			return v
		}
		var v *raster
		if idx, err := s.tileIndex(ctx, false); err == nil {
			if _, listed := idx[nid]; !listed {
				if !inKnownGap(nid) {
					v = seaRaster(nid)
				}
			} else {
				v = s.neighbourTile(ctx, nid)
			}
		}
		got[nid] = v
		return v
	})
}

// memGetLocked returns a decoded tile from memory, marking it used.
func (s *Service) memGetLocked(id tileID) *raster {
	r := s.mem.get(id)
	if r != nil {
		if e, ok := s.disk[id]; ok {
			e.used = s.now()
		}
	}
	return r
}

// tileIndex returns the set of listed tiles, loading it once. Unless patient,
// a caller does not wait on a load that has been running for the join
// window or longer (see Config.JoinWindow).
func (s *Service) tileIndex(ctx context.Context, patient bool) (map[tileID]struct{}, error) {
	s.mu.Lock()
	if s.index != nil {
		idx := s.index
		s.mu.Unlock()
		return idx, nil
	}
	if !s.indexFailed.IsZero() && s.now().Sub(s.indexFailed) < failureTTL {
		s.mu.Unlock()
		return nil, errors.New("terrain: tile list fetch failed recently")
	}
	f := s.indexLoading
	if f != nil && !patient && f.closed(s.now(), s.cfg.JoinWindow) {
		age := s.now().Sub(f.started)
		s.mu.Unlock()
		return nil, errStillLoading("the tile list", age)
	}
	if f == nil {
		f = newFlight[map[tileID]struct{}](s.now())
		s.indexLoading = f
		go func() {
			idx, err := s.loadIndex()
			s.mu.Lock()
			s.indexLoading = nil
			if err != nil {
				s.indexFailed = s.now()
				s.log.Warn("terrain tile list unavailable", zap.Error(err))
			} else {
				s.index = idx
			}
			s.mu.Unlock()
			f.val, f.err = idx, err
			close(f.done)
		}()
	}
	s.mu.Unlock()
	return f.wait(ctx)
}

// tile returns a decoded listed tile, sharing one load among all callers.
// Unless patient, a caller does not wait on a load that has been running
// for the join window or longer (see Config.JoinWindow).
func (s *Service) tile(ctx context.Context, id tileID, patient bool) (*raster, error) {
	s.mu.Lock()
	if r := s.memGetLocked(id); r != nil { // landed since the caller looked
		s.mu.Unlock()
		return r, nil
	}
	if at, ok := s.failed[id]; ok {
		if s.now().Sub(at) < failureTTL {
			s.mu.Unlock()
			return nil, fmt.Errorf("terrain: %s failed recently", id.key())
		}
		delete(s.failed, id)
	}
	f, started := s.startLoadLocked(id)
	if !started && !patient && f.closed(s.now(), s.cfg.JoinWindow) {
		age := s.now().Sub(f.started)
		s.mu.Unlock()
		return nil, errStillLoading(id.key(), age)
	}
	s.mu.Unlock()
	return f.wait(ctx)
}

// startLoadLocked returns the load in flight for a tile, starting one if
// there is none, and whether it started it. The caller holds s.mu.
func (s *Service) startLoadLocked(id tileID) (f *flight[*raster], started bool) {
	f = s.loading[id]
	if f == nil {
		f = newFlight[*raster](s.now())
		s.loading[id] = f
		go s.runLoad(id, f)
		started = true
	}
	return f, started
}

// neighbourTile returns a listed neighbouring tile without waiting on the
// network: from memory, or decoded from the disk cache within ctx. A tile
// that is not cached has its download started in the background (shared,
// on its own FetchTimeout) and nil is returned, so the caller uses its edge
// value now. A tile that failed recently is not retried here.
func (s *Service) neighbourTile(ctx context.Context, id tileID) *raster {
	s.mu.Lock()
	if r := s.memGetLocked(id); r != nil {
		s.mu.Unlock()
		return r
	}
	if at, ok := s.failed[id]; ok && s.now().Sub(at) < failureTTL {
		s.mu.Unlock()
		return nil
	}
	_, onDisk := s.disk[id]
	onDisk = onDisk && s.diskOK
	f, started := s.startLoadLocked(id)
	closed := !started && f.closed(s.now(), s.cfg.JoinWindow)
	s.mu.Unlock()
	if !onDisk || closed {
		return nil
	}
	r, err := f.wait(ctx)
	if err != nil {
		return nil
	}
	return r
}

func (s *Service) runLoad(id tileID, f *flight[*raster]) {
	r, err := s.loadTile(id)
	s.mu.Lock()
	delete(s.loading, id)
	if err != nil {
		s.failed[id] = s.now()
		s.failures.Add(1)
		s.log.Warn("terrain tile unavailable", zap.String("tile", id.key()), zap.Error(err))
	} else {
		s.mem.put(id, r)
	}
	s.mu.Unlock()
	f.val, f.err = r, err
	close(f.done)
}

// Preload makes sure every listed tile in a box is cached, downloading what
// is missing, and returns how many listed tiles the box holds that are now
// ready. Sea and known gaps are not counted. It waits within ctx, on
// downloads of any age (the join window does not apply); downloads still
// running when ctx ends carry on.
func (s *Service) Preload(ctx context.Context, south, west, north, east float64) (int, error) {
	if s.static != nil {
		return 0, nil
	}
	idx, err := s.tileIndex(ctx, true)
	if err != nil {
		return 0, unavailable(err)
	}
	ready := 0
	var errs []error
	for _, id := range tilesInBox(south, west, north, east) {
		if _, listed := idx[id]; !listed {
			continue
		}
		s.mu.Lock()
		_, onDisk := s.disk[id]
		inMem := s.mem.byID[id] != nil
		onDisk = onDisk && s.diskOK
		s.mu.Unlock()
		if onDisk || inMem {
			ready++
			continue
		}
		if _, err := s.tile(ctx, id, true); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id.key(), err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		ready++
	}
	if len(errs) > 0 {
		return ready, unavailable(errors.Join(errs...))
	}
	return ready, nil
}

// Stats returns the counters so far.
func (s *Service) Stats() Stats {
	st := Stats{
		Hits:      s.hits.Load(),
		Misses:    s.misses.Load(),
		Downloads: s.downloads.Load(),
		Failures:  s.failures.Load(),
	}
	s.mu.Lock()
	st.CacheBytes = s.diskBytes
	s.mu.Unlock()
	return st
}
