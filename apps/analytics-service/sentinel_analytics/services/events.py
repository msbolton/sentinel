"""Wire-contract helpers: decode entity events and encode alert events.

Kept free of Kafka and application imports so it is testable on its own.
"""

from __future__ import annotations

import uuid
from datetime import datetime, timezone
from typing import Any

from google.protobuf.timestamp_pb2 import Timestamp

from sentinel.v1 import alert_pb2, common_pb2, entity_pb2, events_pb2

PRODUCER = "analytics-service"
SCHEMA_VERSION = "1"

# Kafka headers every protobuf message on the bus carries.
HEADER_CONTENT_TYPE = "content-type"
HEADER_SCHEMA = "schema"
HEADER_SCHEMA_VERSION = "schema-version"
HEADER_PRODUCER = "producer"
CONTENT_TYPE_PROTOBUF = "application/x-protobuf"


def _enum_name(descriptor, value: int, prefix: str) -> str:
    """Bare enum value name (ENTITY_TYPE_AIRCRAFT -> AIRCRAFT)."""
    try:
        full = descriptor.Name(value)
    except ValueError:
        return "UNKNOWN"
    return full[len(prefix):] if full.startswith(prefix) else full


def position_from_event(data: bytes) -> dict[str, Any] | None:
    """Decode an EntityEvent and return the position payload the detector
    reads, or None when the event is not a position."""
    ev = events_pb2.EntityEvent.FromString(data)
    if ev.WhichOneof("event") != "position":
        return None
    entity = ev.position.entity
    if not entity.id or not entity.HasField("position"):
        return None
    observed = None
    ref = ev.position.observation
    if ref.HasField("source_timestamp"):
        observed = ref.source_timestamp.ToDatetime(tzinfo=timezone.utc)
    elif entity.HasField("last_observed_at"):
        observed = entity.last_observed_at.ToDatetime(tzinfo=timezone.utc)
    elif ev.HasField("event_time"):
        observed = ev.event_time.ToDatetime(tzinfo=timezone.utc)
    return {
        "entity_id": entity.id,
        "entity_type": _enum_name(entity_pb2.EntityType, entity.entity_type, "ENTITY_TYPE_"),
        "latitude": entity.position.latitude,
        "longitude": entity.position.longitude,
        "speed_knots": entity.speed_knots if entity.HasField("speed_knots") else 0.0,
        "heading": entity.heading if entity.HasField("heading") else 0.0,
        "timestamp": observed.isoformat() if observed else None,
        "feed_id": ref.feed_id or entity.last_feed_id,
    }


_ALERT_TYPES = {
    "speed": alert_pb2.ALERT_TYPE_SPEED_ANOMALY,
    "location": alert_pb2.ALERT_TYPE_PATTERN_DEVIATION,
    "pattern_deviation": alert_pb2.ALERT_TYPE_PATTERN_DEVIATION,
    "timing": alert_pb2.ALERT_TYPE_PATTERN_DEVIATION,
    "proximity": alert_pb2.ALERT_TYPE_PROXIMITY,
}

_SEVERITIES = {
    "INFO": alert_pb2.ALERT_SEVERITY_INFO,
    "LOW": alert_pb2.ALERT_SEVERITY_LOW,
    "MEDIUM": alert_pb2.ALERT_SEVERITY_MEDIUM,
    "HIGH": alert_pb2.ALERT_SEVERITY_HIGH,
    "CRITICAL": alert_pb2.ALERT_SEVERITY_CRITICAL,
}


def anomaly_alert_event(
    *,
    entity_id: str,
    anomaly_type: str,
    severity: str,
    score: float,
    description: str,
    expected_value: float | None,
    actual_value: float | None,
    position: dict | None,
    detected_at: datetime,
    alert_id: str | None = None,
) -> alert_pb2.AlertEvent:
    """Build the AlertEvent the alert service persists and the gateway shows."""
    created = Timestamp()
    if detected_at.tzinfo is None:
        detected_at = detected_at.replace(tzinfo=timezone.utc)
    created.FromDatetime(detected_at)
    alert = alert_pb2.Alert(
        alert_id=alert_id or str(uuid.uuid4()),
        alert_type=_ALERT_TYPES.get(anomaly_type.lower(), alert_pb2.ALERT_TYPE_PATTERN_DEVIATION),
        severity=_SEVERITIES.get(severity.upper(), alert_pb2.ALERT_SEVERITY_MEDIUM),
        title=f"{anomaly_type.replace('_', ' ').title()} anomaly",
        description=description,
        entity_id=entity_id,
        created_at=created,
        metadata={"score": f"{score:.3f}", "anomalyType": anomaly_type},
    )
    if position and "latitude" in position and "longitude" in position:
        alert.position.CopyFrom(common_pb2.Coordinate(latitude=float(position["latitude"]), longitude=float(position["longitude"])))
    ev = alert_pb2.AlertEvent(
        alert=alert,
        event_type=alert_pb2.ALERT_EVENT_TYPE_TRIGGERED,
        producer=PRODUCER,
        anomaly_type=anomaly_type,
        score=score,
        expected_value=expected_value or 0.0,
        actual_value=actual_value or 0.0,
        event_time=created,
    )
    return ev


def proto_headers(message) -> list[tuple[str, bytes]]:
    """Standard headers for a protobuf message."""
    return [
        (HEADER_CONTENT_TYPE, CONTENT_TYPE_PROTOBUF.encode()),
        (HEADER_SCHEMA, message.DESCRIPTOR.full_name.encode()),
        (HEADER_SCHEMA_VERSION, SCHEMA_VERSION.encode()),
        (HEADER_PRODUCER, PRODUCER.encode()),
    ]
