-- =============================================================================
-- SENTINEL schema: GIS views for GeoServer
-- =============================================================================
-- Part of db/schema/, applied in file-name order by the db-schema compose
-- service and `make db-schema`. Every statement is idempotent: re-applying
-- the directory after an edit is the way the schema changes before the first
-- release. Column names are camelCase (quoted) where the Go stores use
-- camelCase. Extensions (postgis, timescaledb, age) are enabled by
-- config/postgres/init-extensions.sql at database initialisation.
-- =============================================================================

-- GeoServer (config/geoserver/provision.sh) publishes these views as WMS and
-- WFS layers. One view per clearance level keeps the ceiling in the database:
-- a GIS account cleared to SECRET reads entities_live_s and cannot reach the
-- TOP_SECRET rows however GeoServer is configured, because the sentinel_gis
-- role has no grant on the entities table itself. Columns are snake_case for
-- GIS clients. CREATE OR REPLACE cannot drop a column: when one goes, drop
-- the views by hand first.
CREATE OR REPLACE VIEW sentinel.entities_live AS
SELECT e.id,
       e."entityType"                                 AS entity_type,
       e.name,
       e.classification,
       sentinel.classification_rank(e.classification) AS classification_rank,
       e."feedId"                                     AS feed_id,
       e."lastFeedId"                                 AS last_feed_id,
       e.source,
       e.affiliation,
       e."trackEnvironment"                           AS track_environment,
       e.heading,
       e."speedKnots"                                 AS speed_knots,
       e.course,
       e.altitude,
       e."milStd2525dSymbol"                          AS sidc,
       e."ageoutState"                                AS ageout_state,
       e."lastObservedAt"                             AS last_observed_at,
       e."lastSeenAt"                                 AS last_seen_at,
       ST_Y(e.position)                               AS lat,
       ST_X(e.position)                               AS lon,
       e.position                                     AS geom
  FROM sentinel.entities e
 WHERE e.deleted = FALSE
   AND e."ageoutState" <> 'AGED_OUT'
   AND e.position IS NOT NULL;

CREATE OR REPLACE VIEW sentinel.entities_live_u  AS SELECT * FROM sentinel.entities_live WHERE classification_rank <= 0;
CREATE OR REPLACE VIEW sentinel.entities_live_c  AS SELECT * FROM sentinel.entities_live WHERE classification_rank <= 1;
CREATE OR REPLACE VIEW sentinel.entities_live_s  AS SELECT * FROM sentinel.entities_live WHERE classification_rank <= 2;
CREATE OR REPLACE VIEW sentinel.entities_live_ts AS SELECT * FROM sentinel.entities_live WHERE classification_rank <= 3;

CREATE OR REPLACE VIEW sentinel.locations_geo AS
SELECT id,
       name,
       description,
       category,
       altitude,
       heading,
       pitch,
       "range"                                                            AS range_m,
       ST_SetSRID(ST_MakePoint(longitude, latitude), 4326)::geometry(Point, 4326) AS geom
  FROM sentinel.locations;

-- GeoTools reads primary keys for views from this table, so WFS feature ids
-- are stable (entities_live_u.<uuid>) instead of row numbers.
CREATE TABLE IF NOT EXISTS sentinel.gt_pk_metadata (
  table_schema  VARCHAR NOT NULL,
  table_name    VARCHAR NOT NULL,
  pk_column     VARCHAR NOT NULL,
  pk_column_idx INTEGER,
  pk_policy     VARCHAR,
  pk_sequence   VARCHAR,
  UNIQUE (table_schema, table_name, pk_column)
);
INSERT INTO sentinel.gt_pk_metadata (table_schema, table_name, pk_column, pk_column_idx, pk_policy, pk_sequence)
SELECT 'sentinel', v, 'id', 0, 'assigned', NULL
  FROM unnest(ARRAY['entities_live', 'entities_live_u', 'entities_live_c', 'entities_live_s', 'entities_live_ts', 'locations_geo']) AS v
ON CONFLICT DO NOTHING;

-- Read-only role for GeoServer. The development password is set here;
-- production changes it with ALTER ROLE and passes GIS_DB_PASSWORD to the
-- provisioning job. Under Helm, initdb creates the role first, without a
-- password (the IF NOT EXISTS below then leaves it alone), so it cannot log in
-- until ALTER ROLE sentinel_gis PASSWORD sets one.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'sentinel_gis') THEN
    CREATE ROLE sentinel_gis LOGIN PASSWORD 'sentinel_gis_dev';
  END IF;
END
$$;
GRANT USAGE ON SCHEMA sentinel TO sentinel_gis;
GRANT SELECT ON sentinel.entities_live, sentinel.entities_live_u, sentinel.entities_live_c, sentinel.entities_live_s, sentinel.entities_live_ts,
                sentinel.locations_geo, sentinel.gt_pk_metadata TO sentinel_gis;
GRANT SELECT ON public.geometry_columns, public.spatial_ref_sys TO sentinel_gis;
