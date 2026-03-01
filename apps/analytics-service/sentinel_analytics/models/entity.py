"""Pydantic models for the analytics domain."""

from __future__ import annotations

from datetime import datetime
from enum import Enum
from typing import Any

from pydantic import BaseModel, Field


# ---------------------------------------------------------------------------
# Entity types
# ---------------------------------------------------------------------------

class EntityType(str, Enum):
    """Canonical entity types tracked by SENTINEL."""

    UNKNOWN = "UNKNOWN"
    PERSON = "PERSON"
    VEHICLE = "VEHICLE"
    VESSEL = "VESSEL"
    AIRCRAFT = "AIRCRAFT"
    FACILITY = "FACILITY"
    EQUIPMENT = "EQUIPMENT"
    UNIT = "UNIT"
    SIGNAL = "SIGNAL"
    CYBER = "CYBER"


# ---------------------------------------------------------------------------
# Position / track point
# ---------------------------------------------------------------------------

class EntityPosition(BaseModel):
    """A single position report for a tracked entity."""

    entity_id: str
    entity_type: EntityType
    latitude: float = Field(..., ge=-90.0, le=90.0)
    longitude: float = Field(..., ge=-180.0, le=180.0)
    altitude: float = 0.0
    heading: float = Field(0.0, ge=0.0, lt=360.0)
    speed_knots: float = Field(0.0, ge=0.0)
    course: float = Field(0.0, ge=0.0, lt=360.0)
    timestamp: datetime


# ---------------------------------------------------------------------------
# Pattern-of-life
# ---------------------------------------------------------------------------

class LocationCluster(BaseModel):
    """A frequently-visited location identified by clustering."""

    latitude: float
    longitude: float
    frequency: int
    avg_duration_minutes: float
    label: str


class TimePattern(BaseModel):
    """A recurring time-of-activity observation."""

    day_of_week: int = Field(..., ge=0, le=6, description="0=Monday, 6=Sunday")
    hour: int = Field(..., ge=0, le=23)
    frequency: int


class PatternOfLife(BaseModel):
    """Composite pattern-of-life analysis result."""

    entity_id: str
    pattern_type: str  # "routine_location", "travel_pattern", "activity_schedule", "composite"
    confidence: float = Field(..., ge=0.0, le=1.0)
    description: str
    locations: list[LocationCluster]
    time_patterns: list[TimePattern]
    travel_patterns: list[dict[str, Any]] = []
    computed_at: datetime


# ---------------------------------------------------------------------------
# Anomaly detection
# ---------------------------------------------------------------------------

class AnomalySeverity(str, Enum):
    LOW = "LOW"
    MEDIUM = "MEDIUM"
    HIGH = "HIGH"
    CRITICAL = "CRITICAL"


class AnomalyResult(BaseModel):
    """Result of anomaly detection for a single entity."""

    entity_id: str
    anomaly_type: str  # "speed", "location", "pattern_deviation", "timing", "proximity"
    severity: AnomalySeverity
    score: float = Field(..., ge=0.0, le=1.0)
    description: str
    expected_value: float | None = None
    actual_value: float | None = None
    position: dict | None = None  # {latitude, longitude}
    detected_at: datetime


# ---------------------------------------------------------------------------
# Entity classification
# ---------------------------------------------------------------------------

class EntityClassification(BaseModel):
    """Predicted entity type based on movement characteristics."""

    entity_id: str
    predicted_type: EntityType
    confidence: float = Field(..., ge=0.0, le=1.0)
    features_used: list[str]
    alternative_types: list[dict] | None = None  # [{type, confidence}]
    classified_at: datetime


# ---------------------------------------------------------------------------
# Predictive analytics
# ---------------------------------------------------------------------------

class PredictionResult(BaseModel):
    """Prediction about an entity's future state."""

    entity_id: str
    prediction_type: str  # "next_location", "arrival_time", "destination"
    confidence: float = Field(..., ge=0.0, le=1.0)
    predicted_position: dict | None = None  # {latitude, longitude, altitude}
    predicted_time: datetime | None = None
    description: str


# ---------------------------------------------------------------------------
# Request / response helpers
# ---------------------------------------------------------------------------

class AnomalyDetectRequest(BaseModel):
    """Request body for on-demand anomaly detection."""

    entity_id: str
    latitude: float = Field(..., ge=-90.0, le=90.0)
    longitude: float = Field(..., ge=-180.0, le=180.0)
    speed_knots: float = Field(0.0, ge=0.0)
    entity_type: EntityType = EntityType.UNKNOWN
    timestamp: datetime | None = None


class PipelineStats(BaseModel):
    """Runtime statistics for the analytics pipeline."""

    positions_processed: int
    anomalies_detected: int
    patterns_computed: int
    classifications_made: int
    predictions_served: int
    uptime_seconds: float
    kafka_consumer_lag: int | None = None
