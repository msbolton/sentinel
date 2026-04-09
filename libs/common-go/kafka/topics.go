package kafka

// Topic constants mirror libs/common/src/kafka-topics.ts exactly.
const (
	TopicEntityPosition = "events.entity.position"
	TopicEntityCreated  = "events.entity.created"
	TopicEntityUpdated  = "events.entity.updated"
	TopicEntityDeleted  = "events.entity.deleted"
	TopicEntityStale    = "events.entity.stale"
	TopicEntityAgedOut  = "events.entity.agedout"
	TopicEntityRestored = "events.entity.restored"
	TopicTrackPoint     = "events.track.point"
	TopicAlertGeofence  = "alerts.geofence"
	TopicAlertAnomaly   = "alerts.anomaly"
	TopicAnalyticsPattern = "analytics.pattern"
	TopicIngestRaw      = "ingest.raw"
)

// Consumer group IDs mirror libs/common/src/kafka-topics.ts exactly.
const (
	GroupAPIGateway     = "sentinel-api-gateway"
	GroupEntityService  = "sentinel-entity-service"
	GroupTrackService   = "sentinel-track-service"
	GroupSearchService  = "sentinel-search-service"
	GroupAlertService   = "sentinel-alert-service"
	GroupAnalyticsService = "sentinel-analytics"
)
