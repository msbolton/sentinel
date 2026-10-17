# GeoServer

GeoServer publishes Sentinel's live picture to GIS clients (QGIS, ArcGIS, Google Earth) as WMS and WFS layers. Nothing is configured by hand: `provision.sh` drives the REST API and is idempotent against the persisted data directory.

```bash
docker compose up geoserver-init      # part of docker compose up; safe to re-run
```

## What it creates

| Object | Detail |
|--------|--------|
| Workspace `sentinel` | WMS and WFS enabled |
| Store `sentinel-postgis` | PostGIS on the read-only database role `sentinel_gis` (created by `db/schema/90_gis_views.sql`), schema `sentinel`, primary keys from `gt_pk_metadata` |
| Layers `entities_live_u`, `entities_live_c`, `entities_live_s`, `entities_live_ts` | One view per clearance level over `sentinel.entities_live` (live, non-deleted entities with a position; snake_case columns, `geom` point) |
| Layer `locations_geo` | Sentinel locations |
| Style `entities` | `entities.sld`: symbol per entity type, aircraft rotated by heading, labels under 1:2,000,000 |
| Roles `ROLE_CLASS_U/C/S/TS` and users `gis-u` … `gis-ts` | One account per clearance level (passwords from `GIS_USER_PASSWORD_*`) |
| Layer rules | `entities_live_s` readable by `ROLE_CLASS_S` and `ROLE_CLASS_TS`, and so on; `locations_geo` by everyone; writes admin only |

The clearance ceiling holds even if GeoServer is misconfigured: `sentinel_gis` has `SELECT` on the views only, never on `sentinel.entities`.

## Using the layers

- QGIS: Layer → Add WFS Layer, URL `http://localhost:8600/geoserver/sentinel/wfs`, authentication Basic with `gis-s` / `gis-s-dev` (dev defaults) lists `entities_live_u`, `_c` and `_s`.
- WFS GetFeature as GeoJSON: `http://localhost:8600/geoserver/sentinel/wfs?service=WFS&version=2.0.0&request=GetFeature&typeNames=sentinel:entities_live_u&outputFormat=application/json`
- WMS GetMap: `http://localhost:8600/geoserver/sentinel/wms?service=WMS&request=GetMap&layers=sentinel:entities_live_u&bbox=-180,-90,180,90&width=1024&height=512&srs=EPSG:4326&format=image/png`

Lightweight consumers that cannot do Basic auth (Google Earth network links, KML imports) use the gateway's `GET /api/v1/export/entities.geojson|.kml` with an API key instead; see `docs/api-gateway.md`.

## Production

Change the `sentinel_gis` password (`ALTER ROLE sentinel_gis PASSWORD '…'`) and pass it as `GIS_DB_PASSWORD`; set `GIS_USER_PASSWORD_U/_C/_S/_TS` and the admin password. The shared per-level accounts are a stopgap until GeoServer's OIDC module maps Keycloak roles onto `ROLE_CLASS_*`.
