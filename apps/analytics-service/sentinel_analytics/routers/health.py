"""Health and readiness endpoints."""

from __future__ import annotations

import structlog
from fastapi import APIRouter
from fastapi.responses import JSONResponse

from sentinel_analytics.db import health_check as db_health_check

logger = structlog.get_logger(__name__)

router = APIRouter()


@router.get("/health")
async def health() -> dict[str, str]:
    """Liveness probe -- always returns 200 if the process is up."""
    return {"status": "ok", "service": "sentinel-analytics"}


@router.get("/ready")
async def readiness() -> JSONResponse:
    """Readiness probe -- verifies downstream dependencies.

    Returns 200 when both the database and Kafka consumer are functional;
    503 otherwise.
    """
    checks: dict[str, bool] = {}

    # Database
    try:
        checks["database"] = await db_health_check()
    except Exception:
        logger.warning("readiness.db_check_failed", exc_info=True)
        checks["database"] = False

    # Kafka consumer is considered healthy if the background task is running
    from sentinel_analytics.services.kafka_consumer import _consumer_task

    checks["kafka_consumer"] = _consumer_task is not None and not _consumer_task.done()

    all_ok = all(checks.values())
    status_code = 200 if all_ok else 503

    return JSONResponse(
        status_code=status_code,
        content={
            "status": "ready" if all_ok else "degraded",
            "checks": checks,
        },
    )
