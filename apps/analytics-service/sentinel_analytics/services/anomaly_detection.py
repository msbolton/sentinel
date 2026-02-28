"""Anomaly detection for entity behaviour.

Compares incoming position reports against historical baselines (rolling
statistics kept in Redis) and pattern-of-life clusters to identify speed,
location, temporal, and proximity anomalies.
"""

from __future__ import annotations

import json
import math
from datetime import datetime
from typing import Any

import numpy as np
import structlog
from redis.asyncio import Redis

from sentinel_analytics.config import Settings, settings
from sentinel_analytics.db import fetch_track_points
from sentinel_analytics.models.entity import (
    AnomalyResult,
    AnomalySeverity,
    EntityType,
)

logger = structlog.get_logger(__name__)


def _severity_from_score(score: float) -> AnomalySeverity:
    """Map a 0-1 anomaly score to a severity enum."""
    if score >= 0.9:
        return AnomalySeverity.CRITICAL
    if score >= 0.7:
        return AnomalySeverity.HIGH
    if score >= 0.4:
        return AnomalySeverity.MEDIUM
    return AnomalySeverity.LOW


def _haversine_km(lat1: float, lon1: float, lat2: float, lon2: float) -> float:
    """Great-circle distance in kilometres."""
    r = 6371.0
    phi1, phi2 = math.radians(lat1), math.radians(lat2)
    dphi = math.radians(lat2 - lat1)
    dlam = math.radians(lon2 - lon1)
    a = math.sin(dphi / 2) ** 2 + math.cos(phi1) * math.cos(phi2) * math.sin(dlam / 2) ** 2
    return r * 2 * math.atan2(math.sqrt(a), math.sqrt(1 - a))


class AnomalyDetector:
    """Detect behavioural anomalies for tracked entities."""

    # Redis key prefixes
    _SPEED_STATS_PREFIX = "sentinel:analytics:speed_stats:"
    _LOCATION_CLUSTERS_PREFIX = "sentinel:analytics:loc_clusters:"
    _TEMPORAL_PREFIX = "sentinel:analytics:temporal:"

    def __init__(
        self,
        redis: Redis | None = None,
        config: Settings | None = None,
    ) -> None:
        self.config = config or settings
        self._redis = redis

    # ------------------------------------------------------------------
    # Redis helpers
    # ------------------------------------------------------------------

    async def _get_redis(self) -> Redis:
        if self._redis is None:
            self._redis = Redis.from_url(self.config.redis_url, decode_responses=True)
        return self._redis

    async def _get_json(self, key: str) -> dict | list | None:
        redis = await self._get_redis()
        raw = await redis.get(key)
        if raw is None:
            return None
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            return None

    async def _set_json(self, key: str, value: Any, ttl_seconds: int = 86400) -> None:
        redis = await self._get_redis()
        await redis.set(key, json.dumps(value), ex=ttl_seconds)

    # ------------------------------------------------------------------
    # Speed anomaly
    # ------------------------------------------------------------------

    async def detect_speed_anomaly(
        self,
        entity_id: str,
        current_speed: float,
        entity_type: EntityType = EntityType.UNKNOWN,
    ) -> AnomalyResult | None:
        """Compare *current_speed* against rolling mean/stddev for the entity.

        Falls back to static per-type thresholds when history is unavailable.
        """
        stats = await self._get_json(f"{self._SPEED_STATS_PREFIX}{entity_id}")

        if stats and stats.get("count", 0) >= 10:
            mean = stats["mean"]
            std = stats["std"]
            if std < 0.01:
                std = 1.0  # avoid division by zero for stationary entities
            z_score = abs(current_speed - mean) / std

            if z_score < self.config.anomaly_threshold:
                return None

            score = min(z_score / (self.config.anomaly_threshold * 2), 1.0)
            severity = _severity_from_score(score)
            return AnomalyResult(
                entity_id=entity_id,
                anomaly_type="speed",
                severity=severity,
                score=round(score, 3),
                description=(
                    f"Speed {current_speed:.1f} kn deviates from rolling mean "
                    f"{mean:.1f} kn (z={z_score:.2f})"
                ),
                expected_value=round(mean, 2),
                actual_value=round(current_speed, 2),
                detected_at=datetime.utcnow(),
            )
        else:
            # Static threshold fallback
            threshold = self.config.anomaly_speed_thresholds.get(
                entity_type.value,
                self.config.anomaly_speed_thresholds["UNKNOWN"],
            )
            if current_speed <= threshold:
                return None

            score = min(current_speed / (threshold * 2), 1.0)
            return AnomalyResult(
                entity_id=entity_id,
                anomaly_type="speed",
                severity=_severity_from_score(score),
                score=round(score, 3),
                description=(
                    f"Speed {current_speed:.1f} kn exceeds {entity_type.value} "
                    f"threshold of {threshold:.0f} kn"
                ),
                expected_value=threshold,
                actual_value=round(current_speed, 2),
                detected_at=datetime.utcnow(),
            )

    async def update_speed_stats(self, entity_id: str, speed: float) -> None:
        """Maintain a rolling mean/std for entity speed in Redis."""
        key = f"{self._SPEED_STATS_PREFIX}{entity_id}"
        stats = await self._get_json(key)

        if stats is None:
            stats = {"mean": speed, "m2": 0.0, "count": 1, "std": 0.0}
        else:
            # Welford's online algorithm
            n = stats["count"] + 1
            delta = speed - stats["mean"]
            new_mean = stats["mean"] + delta / n
            delta2 = speed - new_mean
            new_m2 = stats["m2"] + delta * delta2
            stats = {
                "mean": new_mean,
                "m2": new_m2,
                "count": n,
                "std": math.sqrt(new_m2 / n) if n > 1 else 0.0,
            }

        await self._set_json(key, stats, ttl_seconds=7 * 86400)

    # ------------------------------------------------------------------
    # Location anomaly
    # ------------------------------------------------------------------

    async def detect_location_anomaly(
        self,
        entity_id: str,
        latitude: float,
        longitude: float,
    ) -> AnomalyResult | None:
        """Check if position lies outside known pattern-of-life clusters."""
        clusters = await self._get_json(f"{self._LOCATION_CLUSTERS_PREFIX}{entity_id}")
        if not clusters:
            return None  # No baseline yet

        # Find distance to nearest cluster centroid
        min_dist = float("inf")
        for cluster in clusters:
            dist = _haversine_km(latitude, longitude, cluster["latitude"], cluster["longitude"])
            min_dist = min(min_dist, dist)

        # Threshold: > 50 km from any known cluster is anomalous
        threshold_km = 50.0
        if min_dist <= threshold_km:
            return None

        score = min(min_dist / (threshold_km * 4), 1.0)
        return AnomalyResult(
            entity_id=entity_id,
            anomaly_type="location",
            severity=_severity_from_score(score),
            score=round(score, 3),
            description=(
                f"Entity is {min_dist:.1f} km from nearest known location cluster "
                f"(threshold {threshold_km:.0f} km)"
            ),
            expected_value=threshold_km,
            actual_value=round(min_dist, 2),
            position={"latitude": latitude, "longitude": longitude},
            detected_at=datetime.utcnow(),
        )

    async def cache_location_clusters(
        self,
        entity_id: str,
        clusters: list[dict[str, Any]],
    ) -> None:
        """Store pattern-of-life clusters in Redis for fast look-up."""
        serialisable = [
            {"latitude": c.get("latitude", c.get("lat")), "longitude": c.get("longitude", c.get("lng"))}
            for c in clusters
        ]
        await self._set_json(
            f"{self._LOCATION_CLUSTERS_PREFIX}{entity_id}",
            serialisable,
            ttl_seconds=24 * 3600,
        )

    # ------------------------------------------------------------------
    # Pattern-deviation anomaly (temporal)
    # ------------------------------------------------------------------

    async def detect_pattern_deviation(
        self,
        entity_id: str,
        timestamp: datetime,
    ) -> AnomalyResult | None:
        """Check if the entity is active outside its normal schedule."""
        temporal = await self._get_json(f"{self._TEMPORAL_PREFIX}{entity_id}")
        if not temporal:
            return None

        dow = timestamp.weekday()
        hour = timestamp.hour
        key = f"{dow}:{hour}"

        freq_map: dict[str, int] = {t["key"]: t["frequency"] for t in temporal}
        total = sum(freq_map.values()) or 1

        current_freq = freq_map.get(key, 0)
        ratio = current_freq / total

        # If this time slot accounts for < 1% of activity, flag it
        if ratio >= 0.01:
            return None

        score = 1.0 - ratio * 100  # closer to 1 = more anomalous
        score = max(0.0, min(score, 1.0))

        day_names = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"]
        return AnomalyResult(
            entity_id=entity_id,
            anomaly_type="pattern_deviation",
            severity=_severity_from_score(score),
            score=round(score, 3),
            description=(
                f"Activity at {day_names[dow]} {hour:02d}:00 is unusual "
                f"({current_freq} prior observations, {ratio:.2%} of total)"
            ),
            detected_at=datetime.utcnow(),
        )

    async def cache_temporal_patterns(
        self,
        entity_id: str,
        patterns: list[dict[str, Any]],
    ) -> None:
        """Store temporal pattern data in Redis."""
        serialisable = [
            {
                "key": f"{p.get('day_of_week', 0)}:{p.get('hour', 0)}",
                "frequency": p.get("frequency", 0),
            }
            for p in patterns
        ]
        await self._set_json(
            f"{self._TEMPORAL_PREFIX}{entity_id}",
            serialisable,
            ttl_seconds=24 * 3600,
        )

    # ------------------------------------------------------------------
    # Proximity anomaly
    # ------------------------------------------------------------------

    async def detect_proximity_anomaly(
        self,
        entity_id: str,
        latitude: float,
        longitude: float,
        nearby_entities: list[dict[str, Any]] | None = None,
    ) -> AnomalyResult | None:
        """Flag unusual proximity between entities.

        If *nearby_entities* is provided (list of dicts with ``entity_id``,
        ``latitude``, ``longitude``), proximity is evaluated against them.
        """
        if not nearby_entities:
            return None

        closest_dist = float("inf")
        closest_id: str | None = None
        for other in nearby_entities:
            if other.get("entity_id") == entity_id:
                continue
            dist = _haversine_km(latitude, longitude, other["latitude"], other["longitude"])
            if dist < closest_dist:
                closest_dist = dist
                closest_id = other["entity_id"]

        # Threshold: entities within 0.5 km that are not usually co-located
        proximity_threshold_km = 0.5
        if closest_dist > proximity_threshold_km or closest_id is None:
            return None

        score = 1.0 - (closest_dist / proximity_threshold_km)
        return AnomalyResult(
            entity_id=entity_id,
            anomaly_type="proximity",
            severity=_severity_from_score(score),
            score=round(score, 3),
            description=(
                f"Entity is {closest_dist * 1000:.0f} m from {closest_id}, "
                f"which may indicate unusual proximity"
            ),
            position={"latitude": latitude, "longitude": longitude},
            detected_at=datetime.utcnow(),
        )

    # ------------------------------------------------------------------
    # Composite detection (run all checks)
    # ------------------------------------------------------------------

    async def detect_all(
        self,
        entity_id: str,
        latitude: float,
        longitude: float,
        speed_knots: float = 0.0,
        entity_type: EntityType = EntityType.UNKNOWN,
        timestamp: datetime | None = None,
    ) -> list[AnomalyResult]:
        """Run every anomaly detector and return a list of findings."""
        ts = timestamp or datetime.utcnow()
        results: list[AnomalyResult] = []

        checks = [
            self.detect_speed_anomaly(entity_id, speed_knots, entity_type),
            self.detect_location_anomaly(entity_id, latitude, longitude),
            self.detect_pattern_deviation(entity_id, ts),
        ]

        for coro in checks:
            try:
                result = await coro
                if result is not None:
                    results.append(result)
            except Exception:
                logger.warning("anomaly_detection.check_failed", entity_id=entity_id, exc_info=True)

        # Update rolling statistics regardless of anomaly status
        try:
            await self.update_speed_stats(entity_id, speed_knots)
        except Exception:
            logger.warning("anomaly_detection.stats_update_failed", entity_id=entity_id, exc_info=True)

        return results
