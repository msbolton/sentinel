/** Kafka topic names used across all services */
export const KafkaTopics = {
  // Entity events
  ENTITY_POSITION: 'events.entity.position',
  ENTITY_CREATED: 'events.entity.created',
  ENTITY_UPDATED: 'events.entity.updated',

  // Track events
  TRACK_POINT: 'events.track.point',

  // Alert events
  ALERT_GEOFENCE: 'alerts.geofence',
  ALERT_ANOMALY: 'alerts.anomaly',

  // Analytics events
  ANALYTICS_PATTERN: 'analytics.pattern',

  // Raw ingest
  INGEST_RAW: 'ingest.raw',
} as const;

export type KafkaTopic = (typeof KafkaTopics)[keyof typeof KafkaTopics];

/** Kafka consumer group IDs */
export const KafkaConsumerGroups = {
  API_GATEWAY: 'sentinel-api-gateway',
  ENTITY_SERVICE: 'sentinel-entity-service',
  TRACK_SERVICE: 'sentinel-track-service',
  SEARCH_SERVICE: 'sentinel-search-service',
  ALERT_SERVICE: 'sentinel-alert-service',
  ANALYTICS_SERVICE: 'sentinel-analytics',
} as const;
