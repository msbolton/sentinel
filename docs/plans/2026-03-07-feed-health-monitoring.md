# SEN-60: Feed Health Monitoring — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add per-feed Prometheus metrics, health-aware API responses, and in-app staleness indicators for external polling feeds.

**Architecture:** Each polling source (OpenSky, adsb.lol, CelesTrak) records success/failure/count via a `FeedHealthRecorder` interface on the feed manager. The manager computes staleness status from configurable thresholds and includes health in the `/feeds` API response. The web UI replaces client-side freshness with server-provided health status.

**Tech Stack:** Go (ingest-service), Prometheus client_golang, NestJS (API gateway), Angular 19 (web)

---

### Task 1: Add Feed Health Prometheus Metrics

**Files:**
- Modify: `apps/ingest-service/internal/metrics/metrics.go`

**Step 1: Add three new metric fields to the Metrics struct and register them**

```go
// Add to the Metrics struct:
FeedLastSuccess  *prometheus.GaugeVec
FeedEntityCount  *prometheus.GaugeVec
FeedErrorsTotal  *prometheus.CounterVec
```

```go
// Add to New():
FeedLastSuccess: promauto.NewGaugeVec(
    prometheus.GaugeOpts{
        Namespace: "sentinel",
        Subsystem: "ingest",
        Name:      "feed_last_success_timestamp",
        Help:      "Unix timestamp of the last successful poll for an external feed.",
    },
    []string{"feed"},
),
FeedEntityCount: promauto.NewGaugeVec(
    prometheus.GaugeOpts{
        Namespace: "sentinel",
        Subsystem: "ingest",
        Name:      "feed_entities_count",
        Help:      "Number of entities returned by the last successful poll.",
    },
    []string{"feed"},
),
FeedErrorsTotal: promauto.NewCounterVec(
    prometheus.CounterOpts{
        Namespace: "sentinel",
        Subsystem: "ingest",
        Name:      "feed_errors_total",
        Help:      "Total number of poll errors for an external feed.",
    },
    []string{"feed"},
),
```

**Step 2: Verify it compiles**

Run: `cd apps/ingest-service && go build ./...`
Expected: No errors

**Step 3: Commit**

```bash
git add apps/ingest-service/internal/metrics/metrics.go
git commit -m "feat(ingest): add per-feed Prometheus metrics for health monitoring"
```

---

### Task 2: Add Staleness Threshold Config

**Files:**
- Modify: `apps/ingest-service/internal/config/config.go`

**Step 1: Add staleness config fields to the Config struct**

```go
// Add after CelesTrak fields:
FeedStaleWarnSec     int // FEED_STALE_WARN_SEC (default: 120)
FeedStaleCriticalSec int // FEED_STALE_CRITICAL_SEC (default: 300)

OpenSkyStalWarnSec      int // OPENSKY_STALE_WARN_SEC (default: 0 = use global)
OpenSkyStaleCriticalSec int // OPENSKY_STALE_CRITICAL_SEC (default: 0 = use global)

ADSBLolStaleWarnSec     int // ADSBLOL_STALE_WARN_SEC (default: 0 = use global)
ADSBLolStaleCriticalSec int // ADSBLOL_STALE_CRITICAL_SEC (default: 0 = use global)

CelesTrakStaleWarnSec     int // CELESTRAK_STALE_WARN_SEC (default: 600)
CelesTrakStaleCriticalSec int // CELESTRAK_STALE_CRITICAL_SEC (default: 900)
```

**Step 2: Add to the Load() function**

```go
// Add after CelesTrak config:
FeedStaleWarnSec:     envOrDefaultInt("FEED_STALE_WARN_SEC", 120),
FeedStaleCriticalSec: envOrDefaultInt("FEED_STALE_CRITICAL_SEC", 300),

OpenSkyStalWarnSec:      envOrDefaultInt("OPENSKY_STALE_WARN_SEC", 0),
OpenSkyStaleCriticalSec: envOrDefaultInt("OPENSKY_STALE_CRITICAL_SEC", 0),

ADSBLolStaleWarnSec:     envOrDefaultInt("ADSBLOL_STALE_WARN_SEC", 0),
ADSBLolStaleCriticalSec: envOrDefaultInt("ADSBLOL_STALE_CRITICAL_SEC", 0),

CelesTrakStaleWarnSec:     envOrDefaultInt("CELESTRAK_STALE_WARN_SEC", 600),
CelesTrakStaleCriticalSec: envOrDefaultInt("CELESTRAK_STALE_CRITICAL_SEC", 900),
```

**Step 3: Verify it compiles**

Run: `cd apps/ingest-service && go build ./...`
Expected: No errors

**Step 4: Commit**

```bash
git add apps/ingest-service/internal/config/config.go
git commit -m "feat(ingest): add staleness threshold config with per-feed overrides"
```

---

### Task 3: Add Feed Health Tracking to Manager

**Files:**
- Modify: `apps/ingest-service/internal/feeds/manager.go`
- Test: `apps/ingest-service/internal/feeds/manager_test.go`

**Step 1: Write failing tests for health recording and status computation**

Add to `manager_test.go`:

```go
func TestRecordSuccess(t *testing.T) {
	mgr := NewManager(testLogger())
	mgr.Register("f1", "Feed1", "test", "d", newMockFactory(&mockListener{}), false)

	now := time.Now()
	mgr.RecordSuccess("f1", 42, now)

	health := mgr.GetHealth("f1")
	if health == nil {
		t.Fatal("expected health to be non-nil")
	}
	if health.EntitiesCount != 42 {
		t.Errorf("expected 42 entities, got %d", health.EntitiesCount)
	}
	if !health.LastSuccessAt.Equal(now) {
		t.Errorf("expected lastSuccessAt=%v, got %v", now, health.LastSuccessAt)
	}
}

func TestRecordError(t *testing.T) {
	mgr := NewManager(testLogger())
	mgr.Register("f1", "Feed1", "test", "d", newMockFactory(&mockListener{}), false)

	mgr.RecordError("f1")
	mgr.RecordError("f1")

	health := mgr.GetHealth("f1")
	if health == nil {
		t.Fatal("expected health to be non-nil")
	}
	if health.ErrorCount != 2 {
		t.Errorf("expected 2 errors, got %d", health.ErrorCount)
	}
}

func TestHealthStatus(t *testing.T) {
	mgr := NewManager(testLogger())
	mgr.SetStaleThresholds("f1", 120, 300)
	mgr.Register("f1", "Feed1", "test", "d", newMockFactory(&mockListener{}), true)

	// No success yet → unknown
	health := mgr.GetHealth("f1")
	if health.Status != "unknown" {
		t.Errorf("expected unknown, got %s", health.Status)
	}

	// Recent success → healthy
	mgr.RecordSuccess("f1", 10, time.Now())
	health = mgr.GetHealth("f1")
	if health.Status != "healthy" {
		t.Errorf("expected healthy, got %s", health.Status)
	}

	// Old success → warn
	mgr.RecordSuccess("f1", 10, time.Now().Add(-150*time.Second))
	health = mgr.GetHealth("f1")
	if health.Status != "warn" {
		t.Errorf("expected warn, got %s", health.Status)
	}

	// Very old success → critical
	mgr.RecordSuccess("f1", 10, time.Now().Add(-400*time.Second))
	health = mgr.GetHealth("f1")
	if health.Status != "critical" {
		t.Errorf("expected critical, got %s", health.Status)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `cd apps/ingest-service && go test ./internal/feeds/ -run "TestRecordSuccess|TestRecordError|TestHealthStatus" -v`
Expected: Compilation errors (methods don't exist yet)

**Step 3: Implement health tracking in manager.go**

Add `FeedHealth` struct and health fields to `feed`:

```go
import "time"

// FeedHealth describes the health state of a feed.
type FeedHealth struct {
	LastSuccessAt time.Time `json:"lastSuccessAt"`
	EntitiesCount int       `json:"entitiesCount"`
	ErrorCount    int64     `json:"errorCount"`
	Status        string    `json:"status"` // healthy, warn, critical, unknown
}

// Add to the feed struct:
type feed struct {
	status      FeedStatus
	factory     func() (sources.Listener, error)
	listener    sources.Listener
	health      FeedHealth
	warnSec     int
	criticalSec int
}
```

Add methods to Manager:

```go
// SetStaleThresholds sets the warn/critical staleness thresholds for a feed.
func (m *Manager) SetStaleThresholds(id string, warnSec, criticalSec int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.byID[id]; ok {
		f.warnSec = warnSec
		f.criticalSec = criticalSec
	}
}

// RecordSuccess records a successful poll for a feed.
func (m *Manager) RecordSuccess(id string, entitiesCount int, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.byID[id]; ok {
		f.health.LastSuccessAt = at
		f.health.EntitiesCount = entitiesCount
	}
}

// RecordError increments the error count for a feed.
func (m *Manager) RecordError(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.byID[id]; ok {
		f.health.ErrorCount++
	}
}

// GetHealth returns the current health for a feed, computing status from thresholds.
func (m *Manager) GetHealth(id string) *FeedHealth {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.byID[id]
	if !ok {
		return nil
	}
	h := f.health
	h.Status = m.computeStatus(f)
	return &h
}

func (m *Manager) computeStatus(f *feed) string {
	if !f.status.Enabled {
		return "unknown"
	}
	if f.health.LastSuccessAt.IsZero() {
		return "unknown"
	}
	age := time.Since(f.health.LastSuccessAt).Seconds()
	if f.criticalSec > 0 && int(age) >= f.criticalSec {
		return "critical"
	}
	if f.warnSec > 0 && int(age) >= f.warnSec {
		return "warn"
	}
	return "healthy"
}
```

Update `List()` to include health:

```go
// ListWithHealth returns feed statuses with health info.
func (m *Manager) ListWithHealth() []FeedStatusWithHealth {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]FeedStatusWithHealth, len(m.feeds))
	for i, f := range m.feeds {
		h := f.health
		h.Status = m.computeStatus(f)
		out[i] = FeedStatusWithHealth{
			FeedStatus: f.status,
			Health:     &h,
		}
	}
	return out
}

// FeedStatusWithHealth combines FeedStatus with health data.
type FeedStatusWithHealth struct {
	FeedStatus
	Health *FeedHealth `json:"health"`
}
```

**Step 4: Run tests to verify they pass**

Run: `cd apps/ingest-service && go test ./internal/feeds/ -v`
Expected: All tests PASS

**Step 5: Commit**

```bash
git add apps/ingest-service/internal/feeds/manager.go apps/ingest-service/internal/feeds/manager_test.go
git commit -m "feat(ingest): add feed health tracking to manager with staleness status"
```

---

### Task 4: Update Feed Handler to Return Health

**Files:**
- Modify: `apps/ingest-service/internal/feeds/handler.go`
- Test: `apps/ingest-service/internal/feeds/handler_test.go`

**Step 1: Write failing test for health in GET /feeds response**

Add to `handler_test.go`:

```go
func TestHandleFeeds_IncludesHealth(t *testing.T) {
	handler, mgr := setupTestHandler()

	mgr.Register("opensky", "OpenSky", "opensky", "OpenSky feed",
		func() (sources.Listener, error) { return &mockListener{}, nil }, true)
	mgr.SetStaleThresholds("opensky", 120, 300)
	mgr.RecordSuccess("opensky", 100, time.Now())

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/feeds", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var feeds []FeedStatusWithHealth
	if err := json.NewDecoder(w.Body).Decode(&feeds); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(feeds) != 1 {
		t.Fatalf("expected 1 feed, got %d", len(feeds))
	}
	if feeds[0].Health == nil {
		t.Fatal("expected health to be present")
	}
	if feeds[0].Health.Status != "healthy" {
		t.Errorf("expected healthy status, got %s", feeds[0].Health.Status)
	}
	if feeds[0].Health.EntitiesCount != 100 {
		t.Errorf("expected 100 entities, got %d", feeds[0].Health.EntitiesCount)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd apps/ingest-service && go test ./internal/feeds/ -run TestHandleFeeds_IncludesHealth -v`
Expected: FAIL (handler returns FeedStatus, not FeedStatusWithHealth)

**Step 3: Update handleFeeds to use ListWithHealth**

In `handler.go`, change `handleFeeds`:

```go
func (h *Handler) handleFeeds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	feeds := h.manager.ListWithHealth()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(feeds); err != nil {
		h.logger.Error("failed to encode feeds response", zap.Error(err))
	}
}
```

**Step 4: Run all feed handler tests**

Run: `cd apps/ingest-service && go test ./internal/feeds/ -v`
Expected: All PASS. Note: existing `TestHandleFeeds_GET` will need its decode target updated to `[]FeedStatusWithHealth` since the response shape changed.

**Step 5: Commit**

```bash
git add apps/ingest-service/internal/feeds/handler.go apps/ingest-service/internal/feeds/handler_test.go
git commit -m "feat(ingest): include health data in GET /feeds response"
```

---

### Task 5: Instrument Polling Sources

**Files:**
- Modify: `apps/ingest-service/internal/sources/opensky.go`
- Modify: `apps/ingest-service/internal/sources/adsblol.go`
- Modify: `apps/ingest-service/internal/sources/celestrak.go`

Each source needs a reference to the feed manager to call `RecordSuccess`/`RecordError`, plus Prometheus metric updates.

**Step 1: Add a `FeedHealthRecorder` interface to avoid circular imports**

Create a minimal interface in the sources package or use a callback approach. Since sources already import metrics, the simplest approach is to add a callback type:

In each listener struct, add a field:

```go
// Add to OpenSkyListener, ADSBLolListener, CelesTrakListener:
onSuccess func(count int, at time.Time)
onError   func()
```

Update each constructor to accept these callbacks. For example in `opensky.go`:

```go
func NewOpenSkyListener(cfg *config.Config, input chan<- *models.IngestMessage, logger *zap.Logger, m *metrics.Metrics, onSuccess func(int, time.Time), onError func()) *OpenSkyListener {
	return &OpenSkyListener{
		// ... existing fields ...
		onSuccess: onSuccess,
		onError:   onError,
	}
}
```

**Step 2: Call callbacks and update Prometheus metrics in poll methods**

In `opensky.go` `poll()` method, after `l.metrics.MessagesReceived...` on success:

```go
// After successful poll:
now := time.Now()
l.metrics.FeedLastSuccess.WithLabelValues("opensky").Set(float64(now.Unix()))
l.metrics.FeedEntityCount.WithLabelValues("opensky").Set(float64(sent))
if l.onSuccess != nil {
    l.onSuccess(sent, now)
}
```

In `pollLoop()` on error, after the existing `MessagesFailed` increment:

```go
l.metrics.FeedErrorsTotal.WithLabelValues("opensky").Inc()
if l.onError != nil {
    l.onError()
}
```

Apply the same pattern to `adsblol.go` (label `"adsblol"`) and `celestrak.go` (label `"celestrak"`).

For CelesTrak, instrument both `refreshTLEs` (error on TLE fetch failure) and `propagateAll` (success with satellite count).

**Step 3: Update main.go feed registration to pass callbacks**

In `cmd/server/main.go`, update the factory functions:

```go
// OpenSky registration:
func() (sources.Listener, error) {
    return sources.NewOpenSkyListener(cfg, pipelineInput, logger, m,
        func(count int, at time.Time) { feedManager.RecordSuccess("opensky", count, at) },
        func() { feedManager.RecordError("opensky") },
    ), nil
},

// adsb-lol registration:
func() (sources.Listener, error) {
    return sources.NewADSBLolListener(cfg, pipelineInput, logger, m,
        func(count int, at time.Time) { feedManager.RecordSuccess("adsb-lol", count, at) },
        func() { feedManager.RecordError("adsb-lol") },
    ), nil
},

// celestrak registration:
func() (sources.Listener, error) {
    return sources.NewCelesTrakListener(cfg, pipelineInput, logger, m,
        func(count int, at time.Time) { feedManager.RecordSuccess("celestrak", count, at) },
        func() { feedManager.RecordError("celestrak") },
    ), nil
},
```

Also add staleness threshold configuration after feed registration:

```go
// After all Register calls:
feedManager.SetStaleThresholds("opensky",
    staleThreshold(cfg.OpenSkyStalWarnSec, cfg.FeedStaleWarnSec),
    staleThreshold(cfg.OpenSkyStaleCriticalSec, cfg.FeedStaleCriticalSec),
)
feedManager.SetStaleThresholds("adsb-lol",
    staleThreshold(cfg.ADSBLolStaleWarnSec, cfg.FeedStaleWarnSec),
    staleThreshold(cfg.ADSBLolStaleCriticalSec, cfg.FeedStaleCriticalSec),
)
feedManager.SetStaleThresholds("celestrak",
    staleThreshold(cfg.CelesTrakStaleWarnSec, cfg.FeedStaleWarnSec),
    staleThreshold(cfg.CelesTrakStaleCriticalSec, cfg.FeedStaleCriticalSec),
)
```

Add helper in main.go:

```go
func staleThreshold(perFeed, global int) int {
	if perFeed > 0 {
		return perFeed
	}
	return global
}
```

**Step 4: Fix existing source tests (update constructor calls)**

The existing tests in `opensky_test.go`, `adsblol_test.go`, `celestrak_test.go` need their `New*Listener` calls updated to pass `nil, nil` for the callbacks.

**Step 5: Verify everything compiles and tests pass**

Run: `cd apps/ingest-service && go test ./... -v`
Expected: All PASS

**Step 6: Commit**

```bash
git add apps/ingest-service/
git commit -m "feat(ingest): instrument polling sources with health recording and Prometheus metrics"
```

---

### Task 6: Update API Gateway to Pass Through Health

**Files:**
- Modify: `apps/api-gateway/src/modules/feeds/feeds.service.ts`

**Step 1: Update the FeedStatus interface to include health**

```typescript
export interface FeedHealth {
  lastSuccessAt: string;
  entitiesCount: number;
  errorCount: number;
  status: 'healthy' | 'warn' | 'critical' | 'unknown';
}

export interface FeedStatus {
  id: string;
  name: string;
  sourceType: string;
  description: string;
  enabled: boolean;
  health?: FeedHealth;
}
```

No other changes needed — the gateway already proxies the full response.

**Step 2: Verify it compiles**

Run: `cd apps/api-gateway && npx nest build` (or whatever the build command is)

**Step 3: Commit**

```bash
git add apps/api-gateway/src/modules/feeds/feeds.service.ts
git commit -m "feat(api-gateway): add FeedHealth type to feeds service"
```

---

### Task 7: Update Web Data Feed Service

**Files:**
- Modify: `apps/web/src/app/core/services/data-feed.service.ts`

**Step 1: Update the DataFeed interface**

```typescript
export interface FeedHealth {
  lastSuccessAt: string;
  entitiesCount: number;
  errorCount: number;
  status: 'healthy' | 'warn' | 'critical' | 'unknown';
}

export interface DataFeed {
  id: string;
  name: string;
  sourceType: string;
  description: string;
  enabled: boolean;
  health?: FeedHealth;
}
```

**Step 2: Verify it compiles**

Run: `cd apps/web && npx ng build` (or the project build command)

**Step 3: Commit**

```bash
git add apps/web/src/app/core/services/data-feed.service.ts
git commit -m "feat(web): add FeedHealth type to data feed service"
```

---

### Task 8: Update Data Feeds Component to Use Server Health

**Files:**
- Modify: `apps/web/src/app/shared/components/data-feeds.component.ts`

**Step 1: Replace client-side freshness with server-provided health**

Update `getFreshnessStatus` to use server health:

```typescript
getFreshnessStatus(feedId: string): 'green' | 'yellow' | 'red' | 'none' {
  const feeds = this.feedService.feeds();
  const feed = feeds.find(f => f.id === feedId);
  if (!feed?.health) return 'none';
  switch (feed.health.status) {
    case 'healthy': return 'green';
    case 'warn': return 'yellow';
    case 'critical': return 'red';
    default: return 'none';
  }
}
```

Update `extractCount` to use server-provided entity count:

```typescript
private extractCount(feed: DataFeed): number | null {
  return feed.health?.entitiesCount ?? null;
}
```

Update `getRelativeTime` to use server-provided lastSuccessAt:

```typescript
private getRelativeTime(feed: DataFeed): string {
  if (!feed.health?.lastSuccessAt) {
    return feed.enabled ? 'waiting...' : 'never';
  }
  const lastSeen = new Date(feed.health.lastSuccessAt);
  const ageSec = Math.floor((Date.now() - lastSeen.getTime()) / 1000);
  if (ageSec < 5) return 'just now';
  if (ageSec < 60) return `${ageSec}s ago`;
  if (ageSec < 3600) return `${Math.floor(ageSec / 60)}m ago`;
  return `${Math.floor(ageSec / 3600)}h ago`;
}
```

Update the layer-meta template to show error count when > 0:

```html
<div class="layer-meta">
  {{ layer.source }} · {{ layer.lastUpdated }}
  @if (getErrorCount(layer.id) > 0) {
    · <span class="error-count">{{ getErrorCount(layer.id) }} errors</span>
  }
</div>
```

Add `getErrorCount` method:

```typescript
getErrorCount(feedId: string): number {
  const feeds = this.feedService.feeds();
  const feed = feeds.find(f => f.id === feedId);
  return feed?.health?.errorCount ?? 0;
}
```

Add CSS for error count:

```css
.error-count {
  color: #ef4444;
}
```

**Step 2: Remove now-unused client-side freshness tracking**

Remove:
- `feedFreshness` signal
- `entitySub` and the `entityService.entityUpdates$` subscription in `ngOnInit`
- `ngOnDestroy` (if only unsubscribing entitySub)
- `FEED_ENTITY_TYPE_MAP`
- `EntityService` and `EntityType` imports (if no longer used)
- `Subscription` import (if no longer used)
- `OnDestroy` import (if no longer used)

**Step 3: Set up a polling interval to refresh feed health**

Add to `ngOnInit`:

```typescript
// Refresh feed health every 30s when panel is open
this.refreshInterval = setInterval(() => {
  if (this.expanded()) {
    this.feedService.loadFeeds();
  }
}, 30_000);
```

Add field and cleanup:

```typescript
private refreshInterval?: ReturnType<typeof setInterval>;

// In ngOnDestroy or add one:
ngOnDestroy(): void {
  if (this.refreshInterval) {
    clearInterval(this.refreshInterval);
  }
}
```

**Step 4: Verify it compiles**

Run: `cd apps/web && npx ng build`

**Step 5: Commit**

```bash
git add apps/web/src/app/shared/components/data-feeds.component.ts
git commit -m "feat(web): use server-provided feed health for status indicators"
```

---

### Task 9: Add gray freshness dot for unknown status

**Files:**
- Modify: `apps/web/src/app/shared/components/data-feeds.component.ts`

**Step 1: Add gray status to CSS**

The current CSS has `green`, `yellow`, `red`, `none`. Update to add `gray`:

```css
&[data-status="gray"]   { background: #6b7280; }
```

**Step 2: Update getFreshnessStatus to return 'gray' for unknown**

Change the `default` case to return `'gray'` instead of `'none'`:

```typescript
case 'unknown': return 'gray';
default: return 'none';
```

And update the return type to include `'gray'`:

```typescript
getFreshnessStatus(feedId: string): 'green' | 'yellow' | 'red' | 'gray' | 'none' {
```

**Step 3: Commit**

```bash
git add apps/web/src/app/shared/components/data-feeds.component.ts
git commit -m "feat(web): add gray freshness dot for unknown feed health status"
```

---

### Task 10: Final Verification

**Step 1: Run all ingest-service tests**

Run: `cd apps/ingest-service && go test ./... -v`
Expected: All PASS

**Step 2: Run web build**

Run: `cd apps/web && npx ng build`
Expected: Build succeeds

**Step 3: Run API gateway build**

Run: `cd apps/api-gateway && npx nest build` (or equivalent)
Expected: Build succeeds

**Step 4: Commit any remaining changes and verify clean state**

```bash
git status
```
Expected: Clean working tree
