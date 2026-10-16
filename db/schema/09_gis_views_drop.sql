-- =============================================================================
-- SENTINEL schema: drop the GIS views before the tables they read change
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release.
-- =============================================================================

-- 10_entities.sql and 40_locations.sql may alter column types, which
-- PostgreSQL refuses while a view depends on the column. The GeoServer views
-- hold no data, so they are dropped here and recreated by 90_gis_views.sql
-- at the end of every apply.
DROP VIEW IF EXISTS sentinel.entities_live_u, sentinel.entities_live_c, sentinel.entities_live_s, sentinel.entities_live_ts;
DROP VIEW IF EXISTS sentinel.entities_live;
DROP VIEW IF EXISTS sentinel.locations_geo;
