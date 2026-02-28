"""Application configuration loaded from environment variables."""

from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    """SENTINEL Analytics Service configuration.

    All values can be overridden via environment variables prefixed with ``SENTINEL_``.
    For example, ``SENTINEL_KAFKA_BROKERS=broker:9092``.
    """

    # Kafka
    kafka_brokers: str = "localhost:9092"
    kafka_group_id: str = "sentinel-analytics"
    kafka_position_topic: str = "events.entity.position"
    kafka_anomaly_topic: str = "alerts.anomaly"

    # PostgreSQL / TimescaleDB
    postgres_dsn: str = "postgresql://sentinel:sentinel_dev@localhost:5432/sentinel"

    # Redis
    redis_url: str = "redis://localhost:6379/0"

    # Anomaly detection
    anomaly_threshold: float = 2.5  # standard deviations
    anomaly_speed_thresholds: dict[str, float] = {
        "PERSON": 15.0,
        "VEHICLE": 200.0,
        "VESSEL": 60.0,
        "AIRCRAFT": 600.0,
        "UNKNOWN": 100.0,
    }

    # Pattern-of-life
    pattern_window_hours: int = 168  # 7 days
    min_points_for_pattern: int = 50
    pattern_recompute_interval_minutes: int = 60

    # Predictive
    prediction_default_horizon_minutes: int = 30

    # Classifier
    classifier_model_path: str = "models/entity_classifier.pkl"
    classifier_retrain_interval_hours: int = 24

    class Config:
        env_prefix = "SENTINEL_"


settings = Settings()
