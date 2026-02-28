"""Entity-type classification from movement characteristics.

Uses a scikit-learn ``RandomForestClassifier`` trained on labelled track data.
When no pre-trained model is available, the classifier falls back to a simple
rule-based heuristic.
"""

from __future__ import annotations

import pickle
from datetime import datetime
from pathlib import Path
from typing import Any

import numpy as np
import structlog
from sklearn.ensemble import RandomForestClassifier

from sentinel_analytics.config import Settings, settings
from sentinel_analytics.db import fetch_track_points
from sentinel_analytics.models.entity import (
    EntityClassification,
    EntityType,
)

logger = structlog.get_logger(__name__)

# Features extracted from track history
FEATURE_NAMES: list[str] = [
    "avg_speed_knots",
    "max_speed_knots",
    "speed_variance",
    "avg_altitude",
    "altitude_variance",
    "heading_change_rate",
    "linearity_ratio",
    "active_hours_spread",
    "total_distance_km",
    "avg_dwell_time_minutes",
]

# Type labels in the order the model was trained on
TYPE_LABELS: list[EntityType] = [
    EntityType.PERSON,
    EntityType.VEHICLE,
    EntityType.VESSEL,
    EntityType.AIRCRAFT,
    EntityType.FACILITY,
    EntityType.EQUIPMENT,
    EntityType.UNIT,
    EntityType.SIGNAL,
    EntityType.CYBER,
    EntityType.UNKNOWN,
]


class EntityClassifier:
    """Predict the type of an entity from its kinematic features."""

    def __init__(self, config: Settings | None = None) -> None:
        self.config = config or settings
        self._model: RandomForestClassifier | None = None
        self._model_loaded = False

    # ------------------------------------------------------------------
    # Model loading
    # ------------------------------------------------------------------

    def _load_model(self) -> bool:
        """Attempt to load a pre-trained model from disk."""
        model_path = Path(self.config.classifier_model_path)
        if not model_path.exists():
            logger.info("classifier.model_not_found", path=str(model_path))
            return False
        try:
            with model_path.open("rb") as fh:
                self._model = pickle.load(fh)  # noqa: S301 -- trusted model file
            self._model_loaded = True
            logger.info("classifier.model_loaded", path=str(model_path))
            return True
        except Exception:
            logger.warning("classifier.model_load_failed", exc_info=True)
            return False

    # ------------------------------------------------------------------
    # Feature extraction
    # ------------------------------------------------------------------

    @staticmethod
    def _extract_features(points: list[dict[str, Any]]) -> np.ndarray:
        """Derive kinematic features from a sequence of track points."""
        if not points:
            return np.zeros(len(FEATURE_NAMES))

        speeds = np.array([p.get("speed_knots", 0.0) for p in points])
        altitudes = np.array([p.get("altitude", 0.0) for p in points])
        headings = np.array([p.get("heading", 0.0) for p in points])
        timestamps = [p["timestamp"] for p in points]

        # Heading change rate (degrees per report)
        heading_diffs = np.abs(np.diff(headings))
        heading_diffs = np.minimum(heading_diffs, 360.0 - heading_diffs)
        heading_change_rate = float(np.mean(heading_diffs)) if len(heading_diffs) > 0 else 0.0

        # Linearity: ratio of displacement to path length
        from sentinel_analytics.services.anomaly_detection import _haversine_km

        total_distance = 0.0
        for i in range(1, len(points)):
            total_distance += _haversine_km(
                points[i - 1]["latitude"],
                points[i - 1]["longitude"],
                points[i]["latitude"],
                points[i]["longitude"],
            )
        displacement = _haversine_km(
            points[0]["latitude"],
            points[0]["longitude"],
            points[-1]["latitude"],
            points[-1]["longitude"],
        ) if len(points) > 1 else 0.0
        linearity = displacement / total_distance if total_distance > 0.01 else 0.0

        # Active hours spread
        hours = {ts.hour for ts in timestamps}
        active_spread = len(hours) / 24.0

        # Dwell-time proxy: count consecutive points with speed < 1 kn
        dwell_count = int(np.sum(speeds < 1.0))
        avg_dwell = (dwell_count / len(points)) * 60.0  # approximate minutes

        features = np.array([
            float(np.mean(speeds)),
            float(np.max(speeds)),
            float(np.var(speeds)),
            float(np.mean(altitudes)),
            float(np.var(altitudes)),
            heading_change_rate,
            linearity,
            active_spread,
            total_distance,
            avg_dwell,
        ])
        return features

    # ------------------------------------------------------------------
    # Classification
    # ------------------------------------------------------------------

    async def classify(
        self,
        entity_id: str,
        window_hours: int = 168,
    ) -> EntityClassification:
        """Classify *entity_id* based on its movement over *window_hours*."""
        points = await fetch_track_points(entity_id, window_hours)
        features = self._extract_features(points)

        if self._model is not None or self._load_model():
            return self._classify_ml(entity_id, features)

        return self._classify_heuristic(entity_id, features)

    def _classify_ml(
        self,
        entity_id: str,
        features: np.ndarray,
    ) -> EntityClassification:
        """Classify using the trained random-forest model."""
        assert self._model is not None
        proba = self._model.predict_proba(features.reshape(1, -1))[0]
        top_idx = int(np.argmax(proba))
        predicted_type = TYPE_LABELS[top_idx] if top_idx < len(TYPE_LABELS) else EntityType.UNKNOWN
        confidence = float(proba[top_idx])

        # Build alternatives (top-3 excluding the winner)
        ranked = sorted(enumerate(proba), key=lambda x: -x[1])
        alternatives = [
            {"type": TYPE_LABELS[i].value, "confidence": round(float(p), 3)}
            for i, p in ranked[1:4]
            if i < len(TYPE_LABELS)
        ]

        return EntityClassification(
            entity_id=entity_id,
            predicted_type=predicted_type,
            confidence=round(confidence, 3),
            features_used=FEATURE_NAMES,
            alternative_types=alternatives,
            classified_at=datetime.utcnow(),
        )

    @staticmethod
    def _classify_heuristic(
        entity_id: str,
        features: np.ndarray,
    ) -> EntityClassification:
        """Rule-based fallback when no ML model is available."""
        avg_speed = features[0]
        max_speed = features[1]
        avg_altitude = features[3]
        linearity = features[6]

        # Simple decision tree
        if avg_altitude > 1000:
            predicted = EntityType.AIRCRAFT
            confidence = min(avg_altitude / 10000, 0.95)
        elif max_speed > 80:
            predicted = EntityType.VEHICLE
            confidence = min(max_speed / 200, 0.85)
        elif max_speed > 30:
            predicted = EntityType.VESSEL
            confidence = 0.55
        elif avg_speed < 5 and linearity < 0.1:
            predicted = EntityType.FACILITY
            confidence = 0.60
        elif avg_speed < 8:
            predicted = EntityType.PERSON
            confidence = 0.50
        else:
            predicted = EntityType.UNKNOWN
            confidence = 0.30

        return EntityClassification(
            entity_id=entity_id,
            predicted_type=predicted,
            confidence=round(confidence, 3),
            features_used=FEATURE_NAMES,
            alternative_types=None,
            classified_at=datetime.utcnow(),
        )
