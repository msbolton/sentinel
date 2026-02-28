"""Analytics API routes.

Exposes pattern-of-life, anomaly detection, entity classification,
and predictive analytics endpoints.
"""

from __future__ import annotations

from datetime import datetime
from typing import Annotated

import structlog
from fastapi import APIRouter, HTTPException, Query

from sentinel_analytics.db import fetch_recent_anomalies, insert_anomaly
from sentinel_analytics.models.entity import (
    AnomalyDetectRequest,
    AnomalyResult,
    EntityClassification,
    PatternOfLife,
    PipelineStats,
    PredictionResult,
)
from sentinel_analytics.services.anomaly_detection import AnomalyDetector
from sentinel_analytics.services.entity_classifier import EntityClassifier
from sentinel_analytics.services.kafka_consumer import get_stats, increment_stat
from sentinel_analytics.services.pattern_of_life import PatternOfLifeAnalyzer
from sentinel_analytics.services.predictive import PredictiveAnalyzer

logger = structlog.get_logger(__name__)

router = APIRouter()

# Shared service instances (stateless, safe to reuse across requests)
_pattern_analyzer = PatternOfLifeAnalyzer()
_anomaly_detector = AnomalyDetector()
_classifier = EntityClassifier()
_predictor = PredictiveAnalyzer()


# ---------------------------------------------------------------------------
# Pattern-of-life
# ---------------------------------------------------------------------------


@router.get(
    "/pattern-of-life/{entity_id}",
    response_model=PatternOfLife,
    summary="Compute pattern-of-life for an entity",
)
async def get_pattern_of_life(
    entity_id: str,
    window_hours: Annotated[int, Query(ge=1, le=8760)] = 168,
) -> PatternOfLife:
    """Analyse the movement history of *entity_id* to identify routine
    locations, travel patterns, and activity schedules.

    Returns a composite pattern-of-life result or 404 when insufficient
    data is available.
    """
    try:
        result = await _pattern_analyzer.analyze(entity_id, window_hours)
    except Exception:
        logger.exception("pattern_of_life.failed", entity_id=entity_id)
        raise HTTPException(status_code=500, detail="Pattern analysis failed")

    if result is None:
        raise HTTPException(
            status_code=404,
            detail=f"Insufficient data to compute pattern of life for entity {entity_id}",
        )

    increment_stat("patterns_computed")
    return result


# ---------------------------------------------------------------------------
# Anomaly detection
# ---------------------------------------------------------------------------


@router.get(
    "/anomalies",
    response_model=list[AnomalyResult],
    summary="List detected anomalies",
)
async def list_anomalies(
    entity_id: Annotated[str | None, Query()] = None,
    start: Annotated[datetime | None, Query()] = None,
    end: Annotated[datetime | None, Query()] = None,
    limit: Annotated[int, Query(ge=1, le=1000)] = 100,
) -> list[AnomalyResult]:
    """Return previously-detected anomalies, optionally filtered by entity
    and time range.
    """
    try:
        rows = await fetch_recent_anomalies(entity_id, start, end, limit)
    except Exception:
        logger.exception("anomalies.list_failed")
        raise HTTPException(status_code=500, detail="Failed to retrieve anomalies")

    results: list[AnomalyResult] = []
    for row in rows:
        position = None
        if row.get("latitude") is not None and row.get("longitude") is not None:
            position = {"latitude": row["latitude"], "longitude": row["longitude"]}
        results.append(
            AnomalyResult(
                entity_id=row["entity_id"],
                anomaly_type=row["anomaly_type"],
                severity=row["severity"],
                score=row["score"],
                description=row["description"],
                expected_value=row.get("expected_value"),
                actual_value=row.get("actual_value"),
                position=position,
                detected_at=row["detected_at"],
            )
        )
    return results


@router.post(
    "/anomalies/detect",
    response_model=list[AnomalyResult],
    summary="Run on-demand anomaly detection",
)
async def detect_anomalies(request: AnomalyDetectRequest) -> list[AnomalyResult]:
    """Trigger anomaly detection for a specific entity position.

    Useful for ad-hoc analysis outside the real-time Kafka pipeline.
    """
    ts = request.timestamp or datetime.utcnow()
    try:
        anomalies = await _anomaly_detector.detect_all(
            entity_id=request.entity_id,
            latitude=request.latitude,
            longitude=request.longitude,
            speed_knots=request.speed_knots,
            entity_type=request.entity_type,
            timestamp=ts,
        )
    except Exception:
        logger.exception("anomalies.detect_failed", entity_id=request.entity_id)
        raise HTTPException(status_code=500, detail="Anomaly detection failed")

    # Persist each detected anomaly
    for anomaly in anomalies:
        try:
            await insert_anomaly({
                "entity_id": anomaly.entity_id,
                "anomaly_type": anomaly.anomaly_type,
                "severity": anomaly.severity.value,
                "score": anomaly.score,
                "description": anomaly.description,
                "expected_value": anomaly.expected_value,
                "actual_value": anomaly.actual_value,
                "latitude": anomaly.position["latitude"] if anomaly.position else 0.0,
                "longitude": anomaly.position["longitude"] if anomaly.position else 0.0,
                "detected_at": anomaly.detected_at,
            })
        except Exception:
            logger.warning("anomalies.persist_failed", entity_id=anomaly.entity_id, exc_info=True)

    increment_stat("anomalies_detected", len(anomalies))
    return anomalies


# ---------------------------------------------------------------------------
# Entity classification
# ---------------------------------------------------------------------------


@router.get(
    "/classify/{entity_id}",
    response_model=EntityClassification,
    summary="Classify entity type from movement",
)
async def classify_entity(
    entity_id: str,
    window_hours: Annotated[int, Query(ge=1, le=8760)] = 168,
) -> EntityClassification:
    """Predict the type of *entity_id* (e.g. VEHICLE, VESSEL, AIRCRAFT)
    based on its kinematic features over the given time window.
    """
    try:
        result = await _classifier.classify(entity_id, window_hours)
    except Exception:
        logger.exception("classify.failed", entity_id=entity_id)
        raise HTTPException(status_code=500, detail="Classification failed")

    increment_stat("classifications_made")
    return result


# ---------------------------------------------------------------------------
# Predictive analytics
# ---------------------------------------------------------------------------


@router.get(
    "/predict/{entity_id}/position",
    response_model=PredictionResult,
    summary="Predict future position",
)
async def predict_position(
    entity_id: str,
    horizon_minutes: Annotated[int, Query(ge=1, le=1440)] = 30,
) -> PredictionResult:
    """Predict where *entity_id* will be in *horizon_minutes*."""
    try:
        result = await _predictor.predict_next_position(entity_id, horizon_minutes)
    except Exception:
        logger.exception("predict.position_failed", entity_id=entity_id)
        raise HTTPException(status_code=500, detail="Position prediction failed")

    increment_stat("predictions_served")
    return result


@router.get(
    "/predict/{entity_id}/destination",
    response_model=PredictionResult,
    summary="Predict likely destination",
)
async def predict_destination(entity_id: str) -> PredictionResult:
    """Predict the most likely destination of *entity_id* based on current
    trajectory and known pattern-of-life locations.
    """
    try:
        # Attempt to load known locations from the pattern-of-life cache
        pattern = await _pattern_analyzer.analyze(entity_id)
        known_locations = None
        if pattern and pattern.locations:
            known_locations = [
                {
                    "latitude": loc.latitude,
                    "longitude": loc.longitude,
                    "frequency": loc.frequency,
                    "label": loc.label,
                }
                for loc in pattern.locations
            ]
    except Exception:
        known_locations = None

    try:
        result = await _predictor.predict_destination(entity_id, known_locations)
    except Exception:
        logger.exception("predict.destination_failed", entity_id=entity_id)
        raise HTTPException(status_code=500, detail="Destination prediction failed")

    increment_stat("predictions_served")
    return result


# ---------------------------------------------------------------------------
# Pipeline statistics
# ---------------------------------------------------------------------------


@router.get(
    "/stats",
    response_model=PipelineStats,
    summary="Analytics pipeline statistics",
)
async def pipeline_stats() -> PipelineStats:
    """Return runtime statistics for the analytics pipeline."""
    raw = get_stats()
    return PipelineStats(
        positions_processed=raw.get("positions_processed", 0),
        anomalies_detected=raw.get("anomalies_detected", 0),
        patterns_computed=raw.get("patterns_computed", 0),
        classifications_made=raw.get("classifications_made", 0),
        predictions_served=raw.get("predictions_served", 0),
        uptime_seconds=raw.get("uptime_seconds", 0.0),
    )
