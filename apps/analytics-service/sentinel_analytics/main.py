"""SENTINEL Analytics Service -- FastAPI application entry-point."""

from contextlib import asynccontextmanager

import structlog
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from sentinel_analytics.config import settings  # noqa: F401
from sentinel_analytics.routers import analytics, health
from sentinel_analytics.services.kafka_consumer import start_consumer, stop_consumer

logger = structlog.get_logger(__name__)


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Manage startup / shutdown of background tasks."""
    logger.info("analytics_service.starting")
    await start_consumer()
    yield
    logger.info("analytics_service.stopping")
    await stop_consumer()


app = FastAPI(
    title="SENTINEL Analytics Service",
    description="ML-driven pattern-of-life, anomaly detection, entity classification, and predictive analytics.",
    version="0.1.0",
    lifespan=lifespan,
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(health.router, tags=["health"])
app.include_router(analytics.router, prefix="/api/v1/analytics", tags=["analytics"])
