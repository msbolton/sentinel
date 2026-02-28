"""Pattern-of-life analysis for tracked entities.

Uses DBSCAN clustering on historical positions and temporal frequency analysis
to build a behavioural baseline for each entity.
"""

from __future__ import annotations

from collections import defaultdict
from datetime import datetime
from typing import Any

import numpy as np
import structlog
from sklearn.cluster import DBSCAN

from sentinel_analytics.config import Settings, settings
from sentinel_analytics.db import fetch_track_points
from sentinel_analytics.models.entity import (
    LocationCluster,
    PatternOfLife,
    TimePattern,
)

logger = structlog.get_logger(__name__)


class PatternOfLifeAnalyzer:
    """Analyse entity movement history to identify routine patterns."""

    def __init__(self, config: Settings | None = None) -> None:
        self.config = config or settings

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def analyze(
        self,
        entity_id: str,
        window_hours: int | None = None,
    ) -> PatternOfLife | None:
        """Compute pattern of life for *entity_id* over the given window.

        Returns ``None`` when insufficient data is available.
        """
        window = window_hours or self.config.pattern_window_hours
        points = await fetch_track_points(entity_id, window)

        if len(points) < self.config.min_points_for_pattern:
            logger.info(
                "pattern.insufficient_data",
                entity_id=entity_id,
                points=len(points),
                required=self.config.min_points_for_pattern,
            )
            return None

        locations = self._cluster_locations(points)
        time_patterns = self._analyze_temporal_patterns(points)
        confidence = self._calculate_confidence(points, locations)
        description = self._generate_description(locations, time_patterns)

        return PatternOfLife(
            entity_id=entity_id,
            pattern_type="composite",
            confidence=confidence,
            description=description,
            locations=locations,
            time_patterns=time_patterns,
            computed_at=datetime.utcnow(),
        )

    # ------------------------------------------------------------------
    # Location clustering
    # ------------------------------------------------------------------

    def _cluster_locations(self, points: list[dict[str, Any]]) -> list[LocationCluster]:
        """Use DBSCAN on lat/lon to find frequently-visited locations."""
        coords = np.array([[p["latitude"], p["longitude"]] for p in points])

        # eps ~100 m expressed in radians for the haversine metric
        clustering = DBSCAN(
            eps=0.001,
            min_samples=5,
            metric="haversine",
        ).fit(np.radians(coords))

        clusters: dict[int, list[dict[str, Any]]] = defaultdict(list)
        for idx, label in enumerate(clustering.labels_):
            if label != -1:
                clusters[label].append(points[idx])

        results: list[LocationCluster] = []
        for label, cluster_points in clusters.items():
            lats = [p["latitude"] for p in cluster_points]
            lngs = [p["longitude"] for p in cluster_points]
            durations = self._estimate_dwell_times(cluster_points)
            results.append(
                LocationCluster(
                    latitude=float(np.mean(lats)),
                    longitude=float(np.mean(lngs)),
                    frequency=len(cluster_points),
                    avg_duration_minutes=float(np.mean(durations)) if durations else 0.0,
                    label=f"Location {label + 1}",
                )
            )

        return sorted(results, key=lambda loc: loc.frequency, reverse=True)

    # ------------------------------------------------------------------
    # Temporal analysis
    # ------------------------------------------------------------------

    def _analyze_temporal_patterns(self, points: list[dict[str, Any]]) -> list[TimePattern]:
        """Identify recurring day-of-week / hour-of-day patterns."""
        freq: dict[tuple[int, int], int] = defaultdict(int)
        for p in points:
            ts: datetime = p["timestamp"]
            freq[(ts.weekday(), ts.hour)] += 1

        sorted_patterns = sorted(freq.items(), key=lambda item: -item[1])
        return [
            TimePattern(day_of_week=dow, hour=hour, frequency=count)
            for (dow, hour), count in sorted_patterns[:20]
        ]

    # ------------------------------------------------------------------
    # Dwell-time estimation
    # ------------------------------------------------------------------

    @staticmethod
    def _estimate_dwell_times(points: list[dict[str, Any]]) -> list[float]:
        """Estimate visit durations within a location cluster.

        A gap of > 30 minutes between consecutive points starts a new visit.
        """
        if len(points) < 2:
            return []

        sorted_pts = sorted(points, key=lambda p: p["timestamp"])
        durations: list[float] = []
        visit_start = sorted_pts[0]["timestamp"]

        for i in range(1, len(sorted_pts)):
            gap_seconds = (sorted_pts[i]["timestamp"] - sorted_pts[i - 1]["timestamp"]).total_seconds()
            if gap_seconds > 1800:  # 30 min gap -> new visit
                duration_min = (sorted_pts[i - 1]["timestamp"] - visit_start).total_seconds() / 60.0
                durations.append(duration_min)
                visit_start = sorted_pts[i]["timestamp"]

        # Close final visit
        final_dur = (sorted_pts[-1]["timestamp"] - visit_start).total_seconds() / 60.0
        durations.append(final_dur)

        # Filter out sub-minute artefacts
        return [d for d in durations if d > 1.0]

    # ------------------------------------------------------------------
    # Travel-pattern identification (placeholder for future work)
    # ------------------------------------------------------------------

    def _identify_travel_patterns(
        self,
        points: list[dict[str, Any]],
        locations: list[LocationCluster],
    ) -> list[dict[str, Any]]:
        """Identify common routes between clustered locations.

        TODO: implement transition-matrix approach.
        """
        return []

    # ------------------------------------------------------------------
    # Confidence & description helpers
    # ------------------------------------------------------------------

    @staticmethod
    def _calculate_confidence(
        points: list[dict[str, Any]],
        locations: list[LocationCluster],
    ) -> float:
        """Heuristic confidence based on data quantity and cluster quality."""
        point_score = min(len(points) / 500.0, 1.0) * 0.5
        location_score = min(len(locations) / 5.0, 1.0) * 0.5
        return round(point_score + location_score, 2)

    @staticmethod
    def _generate_description(
        locations: list[LocationCluster],
        time_patterns: list[TimePattern],
    ) -> str:
        day_names = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"]
        n_locs = len(locations)
        desc = f"Entity frequents {n_locs} distinct location(s)."
        if time_patterns:
            top = time_patterns[0]
            desc += f" Most active: {day_names[top.day_of_week]} at {top.hour:02d}:00."
        return desc
