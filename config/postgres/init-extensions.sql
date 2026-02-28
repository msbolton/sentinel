-- =============================================================================
-- SENTINEL - PostGIS Database Initialization
-- =============================================================================
-- Enables required extensions and initializes schemas.
-- PostGIS is pre-installed in the base image.
-- TimescaleDB and AGE are conditionally enabled only if available.
-- =============================================================================

-- Enable core geospatial extensions (pre-installed in postgis/postgis image)
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS postgis_topology;

-- Conditionally enable TimescaleDB if installed
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'timescaledb') THEN
    CREATE EXTENSION IF NOT EXISTS timescaledb;
    RAISE NOTICE 'TimescaleDB enabled.';
  ELSE
    RAISE NOTICE 'TimescaleDB not available — hypertables will be regular tables.';
  END IF;
END
$$;

-- Conditionally enable Apache AGE if installed
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'age') THEN
    CREATE EXTENSION IF NOT EXISTS age;
    LOAD 'age';
    SET search_path = ag_catalog, "$user", public;
    PERFORM create_graph('entity_graph');
    RAISE NOTICE 'Apache AGE enabled with entity_graph.';
  ELSE
    RAISE NOTICE 'Apache AGE not available — link analysis will use BFS fallback.';
  END IF;
END
$$;

-- Create the sentinel application schema
CREATE SCHEMA IF NOT EXISTS sentinel;

-- Grant usage on schemas to the sentinel user
GRANT USAGE ON SCHEMA sentinel TO sentinel;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA sentinel TO sentinel;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA sentinel TO sentinel;

-- Conditionally grant ag_catalog access
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = 'ag_catalog') THEN
    GRANT USAGE ON SCHEMA ag_catalog TO sentinel;
  END IF;
END
$$;
