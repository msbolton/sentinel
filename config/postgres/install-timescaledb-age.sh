#!/usr/bin/env bash
# =============================================================================
# SENTINEL - PostgreSQL Extension Installer (Dev Mode)
# =============================================================================
# PostGIS is pre-installed in the base image. TimescaleDB and AGE require
# root-level package installation which isn't available in init scripts.
# This script is a no-op for dev; extensions are enabled in init-extensions.sql.
# =============================================================================

echo "============================================="
echo " SENTINEL - Dev mode: PostGIS pre-installed"
echo " TimescaleDB/AGE skipped (install via Dockerfile for prod)"
echo "============================================="
