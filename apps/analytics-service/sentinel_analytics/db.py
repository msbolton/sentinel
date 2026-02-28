"""Database connection pool and helper utilities for TimescaleDB / PostGIS."""

from __future__ import annotations

import asyncio
from contextlib import asynccontextmanager
from datetime import datetime, timedelta
from typing import Any

import psycopg2
import psycopg2.extras
import psycopg2.pool
import structlog

from sentinel_analytics.config import settings

logger = structlog.get_logger(__name__)

# ---------------------------------------------------------------------------
# Connection pool (module-level singleton)
# ---------------------------------------------------------------------------

_pool: psycopg2.pool.ThreadedConnectionPool | None = None


def _get_pool() -> psycopg2.pool.ThreadedConnectionPool:
    """Lazily initialise and return the connection pool."""
    global _pool
    if _pool is None or _pool.closed:
        logger.info("db.pool.creating", dsn=settings.postgres_dsn[:40] + "...")
        _pool = psycopg2.pool.ThreadedConnectionPool(
            minconn=2,
            maxconn=10,
            dsn=settings.postgres_dsn,
        )
    return _pool


def close_pool() -> None:
    """Shut down the connection pool gracefully."""
    global _pool
    if _pool is not None and not _pool.closed:
        _pool.closeall()
        logger.info("db.pool.closed")
        _pool = None


@asynccontextmanager
async def get_connection():
    """Yield a connection from the pool, returning it on exit.

    Usage::

        async with get_connection() as conn:
            with conn.cursor() as cur:
                cur.execute("SELECT 1")
    """
    pool = _get_pool()
    conn = await asyncio.to_thread(pool.getconn)
    try:
        yield conn
    finally:
        await asyncio.to_thread(pool.putconn, conn)


# ---------------------------------------------------------------------------
# Query helpers
# ---------------------------------------------------------------------------

async def fetch_all(
    query: str,
    params: tuple | dict | None = None,
) -> list[dict[str, Any]]:
    """Execute *query* and return all rows as a list of dicts."""
    async with get_connection() as conn:
        with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
            await asyncio.to_thread(cur.execute, query, params)
            rows = await asyncio.to_thread(cur.fetchall)
            return [dict(row) for row in rows]


async def fetch_one(
    query: str,
    params: tuple | dict | None = None,
) -> dict[str, Any] | None:
    """Execute *query* and return the first row as a dict, or ``None``."""
    async with get_connection() as conn:
        with conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor) as cur:
            await asyncio.to_thread(cur.execute, query, params)
            row = await asyncio.to_thread(cur.fetchone)
            return dict(row) if row else None


async def execute(
    query: str,
    params: tuple | dict | None = None,
) -> int:
    """Execute a write query and return the number of affected rows."""
    async with get_connection() as conn:
        with conn.cursor() as cur:
            await asyncio.to_thread(cur.execute, query, params)
            conn.commit()
            return cur.rowcount


# ---------------------------------------------------------------------------
# Domain-specific helpers
# ---------------------------------------------------------------------------

async def fetch_track_points(
    entity_id: str,
    window_hours: int,
) -> list[dict[str, Any]]:
    """Retrieve recent track points for an entity from TimescaleDB."""
    query = """
        SELECT
            entity_id,
            ST_Y(position::geometry) AS latitude,
            ST_X(position::geometry) AS longitude,
            altitude,
            heading,
            speed_knots,
            course,
            "timestamp"
        FROM sentinel.track_points
        WHERE entity_id = %s
          AND "timestamp" >= NOW() - INTERVAL '%s hours'
        ORDER BY "timestamp" ASC
    """
    return await fetch_all(query, (entity_id, window_hours))


async def fetch_recent_anomalies(
    entity_id: str | None,
    start: datetime | None,
    end: datetime | None,
    limit: int = 100,
) -> list[dict[str, Any]]:
    """Retrieve stored anomalies, optionally filtered by entity and time range."""
    clauses: list[str] = []
    params: list[Any] = []

    if entity_id:
        clauses.append("entity_id = %s")
        params.append(entity_id)
    if start:
        clauses.append("detected_at >= %s")
        params.append(start)
    if end:
        clauses.append("detected_at <= %s")
        params.append(end)

    where = "WHERE " + " AND ".join(clauses) if clauses else ""
    query = f"""
        SELECT entity_id, anomaly_type, severity, score,
               description, expected_value, actual_value,
               ST_Y(position::geometry) AS latitude,
               ST_X(position::geometry) AS longitude,
               detected_at
        FROM sentinel.anomalies
        {where}
        ORDER BY detected_at DESC
        LIMIT %s
    """
    params.append(limit)
    return await fetch_all(query, tuple(params))


async def insert_anomaly(anomaly: dict[str, Any]) -> None:
    """Persist a detected anomaly."""
    query = """
        INSERT INTO sentinel.anomalies
            (entity_id, anomaly_type, severity, score, description,
             expected_value, actual_value, position, detected_at)
        VALUES
            (%s, %s, %s, %s, %s, %s, %s,
             ST_SetSRID(ST_MakePoint(%s, %s), 4326), %s)
    """
    lat = anomaly.get("latitude", 0.0)
    lng = anomaly.get("longitude", 0.0)
    await execute(query, (
        anomaly["entity_id"],
        anomaly["anomaly_type"],
        anomaly["severity"],
        anomaly["score"],
        anomaly["description"],
        anomaly.get("expected_value"),
        anomaly.get("actual_value"),
        lng, lat,
        anomaly["detected_at"],
    ))


async def health_check() -> bool:
    """Return ``True`` if the database is reachable."""
    try:
        row = await fetch_one("SELECT 1 AS ok")
        return row is not None and row.get("ok") == 1
    except Exception:
        logger.warning("db.health_check.failed", exc_info=True)
        return False
