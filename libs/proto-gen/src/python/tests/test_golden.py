"""Decode the Go-written golden fixtures to prove the Python package reads
what the Go services write."""

from pathlib import Path

from sentinel.v1 import alert_pb2, common_pb2, entity_pb2, events_pb2, observation_pb2

TESTDATA = Path(__file__).resolve().parents[3] / "testdata"


def test_observation_fixture():
    obs = observation_pb2.Observation.FromString((TESTDATA / "observation.bin").read_bytes())
    assert obs.observation_id == "3b2a7c1e-4f55-5a6b-8c9d-0e1f2a3b4c5d"
    assert obs.feed_id == "opensky"
    assert obs.source == entity_pb2.ENTITY_SOURCE_OPENSKY
    assert obs.entity_type == entity_pb2.ENTITY_TYPE_AIRCRAFT
    assert obs.identities[0].type == common_pb2.IDENTITY_TYPE_ICAO24
    assert obs.identities[0].value == "A1B2C3"
    assert obs.position.altitude_meters == 10668
    assert obs.HasField("heading") and abs(obs.heading - 270.5) < 1e-9
    assert not obs.HasField("course")
    assert obs.source_timestamp.ToJsonString() == "2026-10-15T12:00:00.500Z"
    assert obs.platform.WhichOneof("detail") == "adsb"
    assert obs.platform.adsb.icao_hex == "A1B2C3"


def test_entity_event_fixture():
    ev = events_pb2.EntityEvent.FromString((TESTDATA / "entity_event.bin").read_bytes())
    assert ev.WhichOneof("event") == "position"
    assert ev.producer == "entity-service"
    assert ev.position.entity.name == "UAL123"
    assert ev.position.entity.platforms["adsb"].adsb.squawk == "1200"
    assert ev.position.observation.feed_id == "opensky"


def test_alert_event_fixture():
    ev = alert_pb2.AlertEvent.FromString((TESTDATA / "alert_event.bin").read_bytes())
    assert ev.event_type == alert_pb2.ALERT_EVENT_TYPE_TRIGGERED
    assert ev.alert.alert_type == alert_pb2.ALERT_TYPE_SPEED_ANOMALY
    assert ev.producer == "analytics-service"
    assert abs(ev.score - 0.93) < 1e-9
