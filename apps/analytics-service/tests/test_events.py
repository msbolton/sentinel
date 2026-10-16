"""Contract tests for the analytics service's Kafka payloads."""

from datetime import datetime, timezone

from google.protobuf.timestamp_pb2 import Timestamp

from sentinel.v1 import alert_pb2, common_pb2, entity_pb2, events_pb2
from sentinel_analytics.services import events


def _position_event(speed: float | None = 450.0) -> bytes:
    ts = Timestamp()
    ts.FromDatetime(datetime(2026, 10, 15, 12, 0, 0, tzinfo=timezone.utc))
    entity = entity_pb2.Entity(
        id="e1",
        entity_type=entity_pb2.ENTITY_TYPE_AIRCRAFT,
        position=common_pb2.Coordinate(latitude=41.9, longitude=-87.9),
        last_feed_id="opensky",
    )
    if speed is not None:
        entity.speed_knots = speed
    ev = events_pb2.EntityEvent(entity_id="e1", producer="entity-service")
    ev.position.entity.CopyFrom(entity)
    ev.position.observation.CopyFrom(events_pb2.ObservationRef(feed_id="adsb-lol", source_timestamp=ts))
    return ev.SerializeToString()


def test_position_from_event():
    payload = events.position_from_event(_position_event())
    assert payload == {
        "entity_id": "e1",
        "entity_type": "AIRCRAFT",
        "latitude": 41.9,
        "longitude": -87.9,
        "speed_knots": 450.0,
        "heading": 0.0,
        "timestamp": "2026-10-15T12:00:00+00:00",
        "feed_id": "adsb-lol",
    }
    assert events.position_from_event(_position_event(speed=None))["speed_knots"] == 0.0


def test_non_position_events_are_ignored():
    ev = events_pb2.EntityEvent(entity_id="e1")
    ev.deleted.bulk = True
    assert events.position_from_event(ev.SerializeToString()) is None


def test_anomaly_alert_event_round_trip():
    ev = events.anomaly_alert_event(
        entity_id="e1", anomaly_type="speed", severity="HIGH", score=0.93, description="too fast",
        expected_value=420.0, actual_value=450.0, position={"latitude": 1.0, "longitude": 2.0},
        detected_at=datetime(2026, 10, 15, 12, 0, 0, tzinfo=timezone.utc), alert_id="a1",
    )
    back = alert_pb2.AlertEvent.FromString(ev.SerializeToString())
    assert back.producer == "analytics-service"
    assert back.alert.alert_id == "a1"
    assert back.alert.alert_type == alert_pb2.ALERT_TYPE_SPEED_ANOMALY
    assert back.alert.severity == alert_pb2.ALERT_SEVERITY_HIGH
    assert back.alert.position.longitude == 2.0
    assert back.score == 0.93 and back.actual_value == 450.0
    assert back.alert.metadata["anomalyType"] == "speed"
    headers = dict(events.proto_headers(ev))
    assert headers["schema"] == b"sentinel.v1.AlertEvent" and headers["producer"] == b"analytics-service"
