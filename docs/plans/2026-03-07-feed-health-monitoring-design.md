# SEN-60: External Feed Health Monitoring — Design

## Overview

Add per-feed health monitoring to the ingest service so operators can see when external data feeds stop updating. Covers Prometheus metrics, a health-aware feeds API, configurable staleness thresholds, and in-app visual indicators.

## Scope

External polling feeds only: OpenSky, adsb.lol, CelesTrak. Built-in listeners (MQTT, STOMP, TCP) already have `active_connections` gauges and are out of scope.

## 1. New Prometheus Metrics

Add to `internal/metrics/metrics.go`, labeled by `feed` (opensky, adsblol, celestrak):

| Metric | Type | Description |
|--------|------|-------------|
| `sentinel_ingest_feed_last_success_timestamp` | Gauge | Unix timestamp of last successful poll |
| `sentinel_ingest_feed_entities_count` | Gauge | Entities returned in last successful poll |
| `sentinel_ingest_feed_errors_total` | Counter | Cumulative poll errors |

Each polling source updates these after each poll cycle — success or failure.

## 2. Feed Health in API Response

Extend `GET /feeds` response with a `health` object:

```json
{
  "id": "opensky",
  "name": "OpenSky Network",
  "enabled": true,
  "health": {
    "lastSuccessAt": "2026-03-07T12:00:00Z",
    "entitiesCount": 1423,
    "errorCount": 2,
    "status": "healthy"
  }
}
```

Status values: `healthy`, `warn`, `critical`, `unknown`. Computed server-side from staleness thresholds.

## 3. Staleness Thresholds

Global defaults with per-feed overrides via env vars:

| Variable | Default | Notes |
|----------|---------|-------|
| `FEED_STALE_WARN_SEC` | 120 | Global warn threshold |
| `FEED_STALE_CRITICAL_SEC` | 300 | Global critical threshold |
| `OPENSKY_STALE_WARN_SEC` | — | Uses global |
| `OPENSKY_STALE_CRITICAL_SEC` | — | Uses global |
| `ADSBLOL_STALE_WARN_SEC` | — | Uses global |
| `ADSBLOL_STALE_CRITICAL_SEC` | — | Uses global |
| `CELESTRAK_STALE_WARN_SEC` | 600 | Longer due to propagation intervals |
| `CELESTRAK_STALE_CRITICAL_SEC` | 900 | Longer due to propagation intervals |

Status logic:
- `unknown` — feed enabled but never polled successfully
- `healthy` — last success within warn threshold
- `warn` — last success between warn and critical thresholds
- `critical` — last success beyond critical threshold

## 4. Web UI Changes

Update the existing data-feeds pill component (`data-feeds.component.ts`):

- Replace client-side freshness calculation with server-provided `health.status`
- Map: `healthy` -> green dot, `warn` -> yellow dot, `critical` -> red dot, `unknown` -> gray dot
- Show `entitiesCount` next to each feed name
- Tooltip or subtitle with last success time and error count

No new components or WebSocket channels needed. The existing polling in `data-feed.service.ts` already refreshes feed state.

## 5. Data Flow

```
Source polls -> updates Prometheus metrics + FeedManager health state
                        |
            GET /feeds returns health info
                        |
            API Gateway proxies to web client
                        |
            Data-feeds pill renders status
```

## 6. Files to Modify

**Ingest service (Go):**
- `internal/metrics/metrics.go` — add 3 new metrics
- `internal/config/config.go` — add staleness threshold config
- `internal/feeds/manager.go` — track health state per feed, compute status
- `internal/feeds/handler.go` — include health in API response
- `internal/sources/opensky.go` — record feed metrics on each poll
- `internal/sources/adsblol.go` — record feed metrics on each poll
- `internal/sources/celestrak.go` — record feed metrics on each poll

**API Gateway (NestJS):**
- `apps/api-gateway/src/modules/feeds/feeds.service.ts` — pass through health fields

**Web (Angular):**
- `apps/web/src/app/core/services/data-feed.service.ts` — update model with health
- `apps/web/src/app/shared/components/data-feeds.component.ts` — use server health status

## Decisions

- **External feeds only** — MQTT/STOMP/TCP have connection gauges already
- **In-app alerts only** — Prometheus alerting rules can be layered on later since the metrics are exposed regardless
- **Per-feed threshold overrides** — Necessary because CelesTrak has fundamentally different polling intervals than ADS-B feeds
- **Server-computed status** — Keeps threshold logic in one place rather than duplicating in frontend
