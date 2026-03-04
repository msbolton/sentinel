-- =============================================================================
-- SENTINEL - Enum Type Updates for Shared Protobuf Schema Migration
-- =============================================================================
-- Adds new enum values introduced by the proto-gen canonical schema.
-- All changes are additive — no existing values are modified or removed.
--
-- Note: TypeORM synchronize: true handles this automatically in dev mode.
-- This script is provided for production deployments and CI pipelines.
-- =============================================================================

-- EntityType: add SENSOR
ALTER TYPE sentinel.entitytype ADD VALUE IF NOT EXISTS 'SENSOR';

-- EntitySource: add sensor-type sources
ALTER TYPE sentinel.entitysource ADD VALUE IF NOT EXISTS 'AIS';
ALTER TYPE sentinel.entitysource ADD VALUE IF NOT EXISTS 'ADS_B';
ALTER TYPE sentinel.entitysource ADD VALUE IF NOT EXISTS 'LINK16';
ALTER TYPE sentinel.entitysource ADD VALUE IF NOT EXISTS 'GPS';
ALTER TYPE sentinel.entitysource ADD VALUE IF NOT EXISTS 'RADAR';

-- AlertSeverity: add INFO level
ALTER TYPE sentinel.alertseverity ADD VALUE IF NOT EXISTS 'INFO';

-- AlertType: add superset values from all services
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'GEOFENCE_BREACH';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'PATTERN_DEVIATION';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'COMMUNICATION_ANOMALY';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'NEW_ENTITY';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'ENTITY_CLASSIFICATION_CHANGE';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'GEOFENCE_ENTRY';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'GEOFENCE_EXIT';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'PATTERN_MATCH';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'CUSTOM';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'PROXIMITY_ALERT';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'ENTITY_LOST';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'PATTERN_DETECTED';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'LINK_CHANGE';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'CLASSIFICATION_CHANGE';
ALTER TYPE sentinel.alerttype ADD VALUE IF NOT EXISTS 'SYSTEM';

-- LinkType: add relationship types from frontend
ALTER TYPE sentinel.linktype ADD VALUE IF NOT EXISTS 'GEOGRAPHIC';
ALTER TYPE sentinel.linktype ADD VALUE IF NOT EXISTS 'FAMILIAL';
ALTER TYPE sentinel.linktype ADD VALUE IF NOT EXISTS 'LOGISTIC';
ALTER TYPE sentinel.linktype ADD VALUE IF NOT EXISTS 'OPERATIONAL';
ALTER TYPE sentinel.linktype ADD VALUE IF NOT EXISTS 'IDENTITY';
