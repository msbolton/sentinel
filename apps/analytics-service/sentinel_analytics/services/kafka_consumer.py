"""Kafka consumer that processes entity position events in the background.

Runs anomaly detection on every incoming position update and publishes
alerts to the anomaly topic when thresholds are exceeded.  Periodically
triggers pattern-of-life recomputation for active entities.
"""

from __future__ import annotations

import asyncio
import json
import time
from datetime import datetime
from typing import Any

import structlog
from confluent_kafka import Consumer, KafkaError, KafkaException, Producer

from sentinel_analytics.config import settings
from sentinel_analytics.models.entity import AnomalyResult, EntityType
from sentinel_analytics.services.anomaly_detection import AnomalyDetector

logger = structlog.get_logger(__name__)

# Module-level state for the background task
_consumer_task: asyncio.Task | None = None
_running = False

# Pipeline statistics (simple counters; consider Prometheus gauges for prod)
_stats: dict[str, int] = {
    "positions_processed": 0,
    "anomalies_detected": 0,
    "patterns_computed": 0,
    "classifications_made": 0,
    "predictions_served": 0,
}
_start_time: float = 0.0


def get_stats() -> dict[str, Any]:
    """Return a snapshot of pipeline statistics."""
    return {
        **_stats,
        "uptime_seconds": round(time.time() - _start_time, 1) if _start_time else 0.0,
    }


def increment_stat(key: str, amount: int = 1) -> None:
    """Increment a pipeline stat counter."""
    _stats[key] = _stats.get(key, 0) + amount


# ---------------------------------------------------------------------------
# Kafka helpers
# ---------------------------------------------------------------------------

def _make_consumer() -> Consumer:
    return Consumer({
        "bootstrap.servers": settings.kafka_brokers,
        "group.id": settings.kafka_group_id,
        "auto.offset.reset": "latest",
        "enable.auto.commit": True,
        "session.timeout.ms": 30_000,
        "max.poll.interval.ms": 300_000,
    })


def _make_producer() -> Producer:
    return Producer({
        "bootstrap.servers": settings.kafka_brokers,
        "linger.ms": 50,
        "batch.num.messages": 100,
    })


def _delivery_callback(err, msg) -> None:  # noqa: ANN001
    if err is not None:
        logger.error("kafka.produce.failed", error=str(err), topic=msg.topic())


# ---------------------------------------------------------------------------
# Background consumer loop
# ---------------------------------------------------------------------------

async def _consume_loop() -> None:
    """Long-running loop that polls Kafka and processes position events."""
    global _running

    consumer: Consumer | None = None
    producer: Producer | None = None
    detector = AnomalyDetector()

    try:
        consumer = _make_consumer()
        consumer.subscribe([settings.kafka_position_topic])
        producer = _make_producer()
        logger.info(
            "kafka_consumer.started",
            topic=settings.kafka_position_topic,
            group=settings.kafka_group_id,
        )

        while _running:
            msg = await asyncio.to_thread(consumer.poll, 1.0)
            if msg is None:
                continue
            if msg.error():
                if msg.error().code() == KafkaError._PARTITION_EOF:
                    continue
                logger.error("kafka_consumer.error", error=msg.error())
                continue

            try:
                payload = json.loads(msg.value().decode("utf-8"))
                await _handle_position_event(payload, detector, producer)
            except json.JSONDecodeError:
                logger.warning("kafka_consumer.invalid_json", offset=msg.offset())
            except Exception:
                logger.exception("kafka_consumer.processing_error", offset=msg.offset())

    except KafkaException:
        logger.exception("kafka_consumer.fatal")
    finally:
        if consumer is not None:
            consumer.close()
        if producer is not None:
            producer.flush(timeout=5)
        logger.info("kafka_consumer.stopped")


async def _handle_position_event(
    payload: dict[str, Any],
    detector: AnomalyDetector,
    producer: Producer,
) -> None:
    """Process a single position event: run anomaly checks, publish alerts."""
    entity_id = payload.get("entity_id")
    if not entity_id:
        return

    _stats["positions_processed"] += 1

    latitude = payload.get("latitude", 0.0)
    longitude = payload.get("longitude", 0.0)
    speed = payload.get("speed_knots", 0.0)
    entity_type_str = payload.get("entity_type", "UNKNOWN")
    timestamp_str = payload.get("timestamp")

    try:
        entity_type = EntityType(entity_type_str)
    except ValueError:
        entity_type = EntityType.UNKNOWN

    timestamp = (
        datetime.fromisoformat(timestamp_str) if timestamp_str else datetime.utcnow()
    )

    anomalies = await detector.detect_all(
        entity_id=entity_id,
        latitude=latitude,
        longitude=longitude,
        speed_knots=speed,
        entity_type=entity_type,
        timestamp=timestamp,
    )

    for anomaly in anomalies:
        _stats["anomalies_detected"] += 1
        _publish_anomaly(producer, anomaly)


def _publish_anomaly(producer: Producer, anomaly: AnomalyResult) -> None:
    """Produce an anomaly alert to Kafka."""
    try:
        value = anomaly.model_dump_json().encode("utf-8")
        producer.produce(
            topic=settings.kafka_anomaly_topic,
            key=anomaly.entity_id.encode("utf-8"),
            value=value,
            callback=_delivery_callback,
        )
        producer.poll(0)  # trigger delivery callbacks
    except Exception:
        logger.exception(
            "kafka_consumer.publish_failed",
            entity_id=anomaly.entity_id,
            anomaly_type=anomaly.anomaly_type,
        )


# ---------------------------------------------------------------------------
# Lifecycle
# ---------------------------------------------------------------------------

async def start_consumer() -> None:
    """Start the Kafka consumer background task."""
    global _consumer_task, _running, _start_time

    if _consumer_task is not None and not _consumer_task.done():
        logger.warning("kafka_consumer.already_running")
        return

    _running = True
    _start_time = time.time()
    _consumer_task = asyncio.create_task(_consume_loop())
    logger.info("kafka_consumer.task_created")


async def stop_consumer() -> None:
    """Signal the consumer loop to stop and wait for it."""
    global _running, _consumer_task

    if _consumer_task is None:
        return

    _running = False
    try:
        await asyncio.wait_for(_consumer_task, timeout=10.0)
    except asyncio.TimeoutError:
        logger.warning("kafka_consumer.stop_timeout")
        _consumer_task.cancel()
    _consumer_task = None
