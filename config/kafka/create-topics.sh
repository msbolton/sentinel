#!/usr/bin/env bash
# =============================================================================
# SENTINEL - Kafka Topic Initialization
# =============================================================================
# Creates all required Kafka topics with appropriate partition counts.
# This script is executed by the kafka-init container after Kafka is healthy.
#
# Partition strategy:
#   12 partitions - High-throughput streams (position updates, raw ingest)
#    6 partitions - Moderate-throughput entity lifecycle events
#    3 partitions - Low-throughput alert and analytics topics
# =============================================================================

set -euo pipefail

KAFKA_BROKER="${KAFKA_BROKER:-kafka:9093}"
REPLICATION_FACTOR="${REPLICATION_FACTOR:-1}"

echo "============================================="
echo " SENTINEL - Kafka Topic Initialization"
echo " Broker: ${KAFKA_BROKER}"
echo " Replication Factor: ${REPLICATION_FACTOR}"
echo "============================================="

# Wait for Kafka to be fully ready
echo "Waiting for Kafka to accept connections..."
until /opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server "${KAFKA_BROKER}" > /dev/null 2>&1; do
  echo "  Kafka not ready yet, retrying in 3s..."
  sleep 3
done
echo "Kafka is ready."

# Define topics: name|partitions|retention_ms
# Retention: 7 days = 604800000ms, 30 days = 2592000000ms
TOPICS=(
  "events.entity.position|12|604800000"
  "events.entity.created|6|2592000000"
  "events.entity.updated|6|2592000000"
  "events.entity.deleted|6|2592000000"
  "events.entity.stale|6|2592000000"
  "events.entity.agedout|6|2592000000"
  "events.entity.restored|6|2592000000"
  "events.track.point|12|604800000"
  "alerts.geofence|3|2592000000"
  "alerts.anomaly|3|2592000000"
  "analytics.pattern|3|2592000000"
  "ingest.raw|12|604800000"
)

create_topic() {
  local topic_name="$1"
  local partitions="$2"
  local retention_ms="$3"

  echo -n "Creating topic '${topic_name}' (partitions=${partitions}, retention=${retention_ms}ms)... "

  if /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "${KAFKA_BROKER}" \
    --describe --topic "${topic_name}" > /dev/null 2>&1; then
    echo "ALREADY EXISTS"
  else
    /opt/kafka/bin/kafka-topics.sh \
      --bootstrap-server "${KAFKA_BROKER}" \
      --create \
      --topic "${topic_name}" \
      --partitions "${partitions}" \
      --replication-factor "${REPLICATION_FACTOR}" \
      --config retention.ms="${retention_ms}" \
      --config cleanup.policy=delete
    echo "CREATED"
  fi
}

echo ""
echo "Creating topics..."
echo "---------------------------------------------"

for topic_def in "${TOPICS[@]}"; do
  IFS='|' read -r name partitions retention <<< "${topic_def}"
  create_topic "${name}" "${partitions}" "${retention}"
done

echo "---------------------------------------------"
echo ""

# List all topics for verification
echo "Listing all topics:"
/opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server "${KAFKA_BROKER}" \
  --list

echo ""
echo "============================================="
echo " Topic initialization complete."
echo "============================================="
