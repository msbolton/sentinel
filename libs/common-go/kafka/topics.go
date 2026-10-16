package kafka

// Topic names. config/kafka/create-topics.sh and the Helm chart's
// kafka.provisioning.topics must list the same names.
const (
	// TopicObservationsRaw carries one sentinel.v1.Observation per report
	// from the ingest service, keyed by ObservationKey.
	TopicObservationsRaw = "observations.raw"
	// TopicObservationsDLQ receives payloads a consumer could not process,
	// with the failure in the "error" header.
	TopicObservationsDLQ = "observations.dlq"
	// TopicEntityEvents carries sentinel.v1.EntityEvent, keyed by entity id.
	TopicEntityEvents = "entities.events"
	// TopicAlertEvents carries sentinel.v1.AlertEvent, keyed by entity id.
	TopicAlertEvents = "alerts.events"
)

// Consumer group ids.
const (
	GroupAPIGateway       = "sentinel-api-gateway"
	GroupEntityService    = "sentinel-entity-service"
	GroupTrackService     = "sentinel-track-service"
	GroupSearchService    = "sentinel-search-service"
	GroupAlertService     = "sentinel-alert-service"
	GroupAnalyticsService = "sentinel-analytics"
)
