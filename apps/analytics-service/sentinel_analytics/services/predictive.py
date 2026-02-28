"""Predictive analytics for entity movement.

Provides short-horizon position extrapolation, destination prediction,
and estimated arrival-time computation.
"""

from __future__ import annotations

import math
from datetime import datetime, timedelta
from typing import Any

import numpy as np
import structlog

from sentinel_analytics.config import Settings, settings
from sentinel_analytics.db import fetch_track_points
from sentinel_analytics.models.entity import PredictionResult

logger = structlog.get_logger(__name__)

_EARTH_RADIUS_KM = 6371.0
_KN_TO_KMH = 1.852


def _haversine_km(lat1: float, lon1: float, lat2: float, lon2: float) -> float:
    """Great-circle distance between two points in kilometres."""
    phi1, phi2 = math.radians(lat1), math.radians(lat2)
    dphi = math.radians(lat2 - lat1)
    dlam = math.radians(lon2 - lon1)
    a = math.sin(dphi / 2) ** 2 + math.cos(phi1) * math.cos(phi2) * math.sin(dlam / 2) ** 2
    return _EARTH_RADIUS_KM * 2 * math.atan2(math.sqrt(a), math.sqrt(1 - a))


def _destination_point(
    lat: float,
    lon: float,
    bearing_deg: float,
    distance_km: float,
) -> tuple[float, float]:
    """Compute the point reached by travelling *distance_km* along *bearing_deg*."""
    phi1 = math.radians(lat)
    lam1 = math.radians(lon)
    brng = math.radians(bearing_deg)
    d_r = distance_km / _EARTH_RADIUS_KM

    phi2 = math.asin(
        math.sin(phi1) * math.cos(d_r) + math.cos(phi1) * math.sin(d_r) * math.cos(brng)
    )
    lam2 = lam1 + math.atan2(
        math.sin(brng) * math.sin(d_r) * math.cos(phi1),
        math.cos(d_r) - math.sin(phi1) * math.sin(phi2),
    )
    return math.degrees(phi2), math.degrees(lam2)


class PredictiveAnalyzer:
    """Predict future entity positions, destinations, and arrival times."""

    def __init__(self, config: Settings | None = None) -> None:
        self.config = config or settings

    # ------------------------------------------------------------------
    # Next position prediction
    # ------------------------------------------------------------------

    async def predict_next_position(
        self,
        entity_id: str,
        horizon_minutes: int | None = None,
    ) -> PredictionResult:
        """Predict where the entity will be in *horizon_minutes*.

        Strategy:
        1. Linear extrapolation from most recent speed + course.
        2. If enough history exists, apply a weighted average that blends
           the linear prediction with a pattern-based centroid.
        """
        horizon = horizon_minutes or self.config.prediction_default_horizon_minutes
        points = await fetch_track_points(entity_id, window_hours=24)

        if not points:
            return PredictionResult(
                entity_id=entity_id,
                prediction_type="next_location",
                confidence=0.0,
                description="Insufficient data for position prediction.",
            )

        latest = points[-1]
        lat = latest["latitude"]
        lon = latest["longitude"]
        speed_kn = latest.get("speed_knots", 0.0)
        course = latest.get("course", latest.get("heading", 0.0))

        # Linear extrapolation
        distance_km = (speed_kn * _KN_TO_KMH) * (horizon / 60.0)
        pred_lat, pred_lon = _destination_point(lat, lon, course, distance_km)

        # Confidence degrades with horizon length and speed variability
        confidence = self._position_confidence(points, horizon)

        # Blend with historical pattern if sufficient data
        if len(points) >= 20:
            blended_lat, blended_lon, blend_conf = self._pattern_blend(
                points, pred_lat, pred_lon, horizon,
            )
            # Weighted average of linear + pattern
            w_linear = 0.6
            w_pattern = 0.4
            pred_lat = w_linear * pred_lat + w_pattern * blended_lat
            pred_lon = w_linear * pred_lon + w_pattern * blended_lon
            confidence = w_linear * confidence + w_pattern * blend_conf

        predicted_time = datetime.utcnow() + timedelta(minutes=horizon)

        return PredictionResult(
            entity_id=entity_id,
            prediction_type="next_location",
            confidence=round(min(confidence, 0.99), 3),
            predicted_position={
                "latitude": round(pred_lat, 6),
                "longitude": round(pred_lon, 6),
                "altitude": latest.get("altitude", 0.0),
            },
            predicted_time=predicted_time,
            description=(
                f"Predicted position in {horizon} min at "
                f"({pred_lat:.4f}, {pred_lon:.4f}) based on "
                f"course {course:.0f} deg, speed {speed_kn:.1f} kn."
            ),
        )

    # ------------------------------------------------------------------
    # Destination prediction
    # ------------------------------------------------------------------

    async def predict_destination(
        self,
        entity_id: str,
        known_locations: list[dict[str, Any]] | None = None,
    ) -> PredictionResult:
        """Predict the entity's most likely destination.

        Uses current trajectory and known pattern-of-life locations.
        """
        points = await fetch_track_points(entity_id, window_hours=6)

        if len(points) < 3:
            return PredictionResult(
                entity_id=entity_id,
                prediction_type="destination",
                confidence=0.0,
                description="Insufficient track data for destination prediction.",
            )

        latest = points[-1]
        course = latest.get("course", latest.get("heading", 0.0))
        lat = latest["latitude"]
        lon = latest["longitude"]

        if not known_locations:
            # Extrapolate 2 hours out as a rough destination
            speed_kn = latest.get("speed_knots", 0.0)
            dist_km = speed_kn * _KN_TO_KMH * 2.0
            dest_lat, dest_lon = _destination_point(lat, lon, course, dist_km)
            return PredictionResult(
                entity_id=entity_id,
                prediction_type="destination",
                confidence=0.2,
                predicted_position={
                    "latitude": round(dest_lat, 6),
                    "longitude": round(dest_lon, 6),
                },
                description="Extrapolated destination (no known locations available).",
            )

        # Score each known location by how well the current trajectory
        # points toward it and how frequently the entity visits it.
        best_score = -1.0
        best_loc: dict[str, Any] | None = None

        for loc in known_locations:
            loc_lat = loc["latitude"]
            loc_lon = loc["longitude"]
            freq = loc.get("frequency", 1)

            # Bearing from current position to candidate location
            bearing = self._bearing(lat, lon, loc_lat, loc_lon)
            angle_diff = abs(bearing - course) % 360
            if angle_diff > 180:
                angle_diff = 360 - angle_diff

            # Score: lower angle difference + higher frequency = better
            angle_score = max(0.0, 1.0 - angle_diff / 90.0)
            freq_score = min(freq / 100.0, 1.0)
            score = 0.7 * angle_score + 0.3 * freq_score

            if score > best_score:
                best_score = score
                best_loc = loc

        if best_loc is None or best_score < 0.1:
            return PredictionResult(
                entity_id=entity_id,
                prediction_type="destination",
                confidence=0.1,
                description="No plausible destination found among known locations.",
            )

        return PredictionResult(
            entity_id=entity_id,
            prediction_type="destination",
            confidence=round(min(best_score, 0.95), 3),
            predicted_position={
                "latitude": best_loc["latitude"],
                "longitude": best_loc["longitude"],
            },
            description=(
                f"Predicted destination: {best_loc.get('label', 'known location')} "
                f"(score {best_score:.2f})"
            ),
        )

    # ------------------------------------------------------------------
    # Arrival-time prediction
    # ------------------------------------------------------------------

    async def predict_arrival_time(
        self,
        entity_id: str,
        dest_lat: float,
        dest_lon: float,
    ) -> PredictionResult:
        """Estimate time to reach (*dest_lat*, *dest_lon*)."""
        points = await fetch_track_points(entity_id, window_hours=6)

        if not points:
            return PredictionResult(
                entity_id=entity_id,
                prediction_type="arrival_time",
                confidence=0.0,
                description="No track data available for arrival-time estimate.",
            )

        latest = points[-1]
        lat = latest["latitude"]
        lon = latest["longitude"]
        speed_kn = latest.get("speed_knots", 0.0)

        dist_km = _haversine_km(lat, lon, dest_lat, dest_lon)
        speed_kmh = speed_kn * _KN_TO_KMH

        if speed_kmh < 0.1:
            return PredictionResult(
                entity_id=entity_id,
                prediction_type="arrival_time",
                confidence=0.1,
                predicted_position={"latitude": dest_lat, "longitude": dest_lon},
                description=(
                    f"Entity is {dist_km:.1f} km from destination but nearly "
                    f"stationary ({speed_kn:.1f} kn)."
                ),
            )

        hours_remaining = dist_km / speed_kmh
        eta = datetime.utcnow() + timedelta(hours=hours_remaining)

        # Confidence inversely proportional to remaining distance and speed variance
        speeds = [p.get("speed_knots", 0.0) for p in points[-20:]]
        speed_std = float(np.std(speeds)) if len(speeds) > 1 else 0.0
        speed_cv = speed_std / speed_kmh if speed_kmh > 0 else 1.0
        confidence = max(0.1, 1.0 - speed_cv) * max(0.3, 1.0 - hours_remaining / 24.0)

        return PredictionResult(
            entity_id=entity_id,
            prediction_type="arrival_time",
            confidence=round(min(confidence, 0.95), 3),
            predicted_position={"latitude": dest_lat, "longitude": dest_lon},
            predicted_time=eta,
            description=(
                f"ETA {eta.isoformat()} ({hours_remaining:.1f} h) at current "
                f"speed {speed_kn:.1f} kn over {dist_km:.1f} km."
            ),
        )

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    @staticmethod
    def _position_confidence(points: list[dict[str, Any]], horizon_min: int) -> float:
        """Heuristic confidence that decays with horizon and speed variance."""
        if not points:
            return 0.0

        speeds = [p.get("speed_knots", 0.0) for p in points[-20:]]
        speed_std = float(np.std(speeds)) if len(speeds) > 1 else 0.0
        mean_speed = float(np.mean(speeds)) if speeds else 0.0

        # Coefficient of variation penalty
        cv = speed_std / mean_speed if mean_speed > 0.1 else 1.0
        cv_factor = max(0.2, 1.0 - cv)

        # Time-horizon penalty
        time_factor = max(0.1, 1.0 - horizon_min / 360.0)

        return cv_factor * time_factor

    @staticmethod
    def _pattern_blend(
        points: list[dict[str, Any]],
        linear_lat: float,
        linear_lon: float,
        horizon_min: int,
    ) -> tuple[float, float, float]:
        """Blend linear prediction with the centroid of recent positions.

        Returns (lat, lon, confidence).
        """
        recent = points[-min(len(points), 50):]
        lats = [p["latitude"] for p in recent]
        lons = [p["longitude"] for p in recent]
        centroid_lat = float(np.mean(lats))
        centroid_lon = float(np.mean(lons))
        spread = float(np.std(lats) + np.std(lons))

        conf = max(0.1, 1.0 - spread * 100)
        return centroid_lat, centroid_lon, conf

    @staticmethod
    def _bearing(lat1: float, lon1: float, lat2: float, lon2: float) -> float:
        """Initial bearing from point 1 to point 2 in degrees."""
        phi1 = math.radians(lat1)
        phi2 = math.radians(lat2)
        dlam = math.radians(lon2 - lon1)
        x = math.sin(dlam) * math.cos(phi2)
        y = math.cos(phi1) * math.sin(phi2) - math.sin(phi1) * math.cos(phi2) * math.cos(dlam)
        return (math.degrees(math.atan2(x, y)) + 360) % 360
