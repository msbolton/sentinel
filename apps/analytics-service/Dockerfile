FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir uv
# Generated sentinel.v1 protobuf package (shared contract).
COPY libs/proto-gen/src/python/ /opt/sentinel-proto/
RUN uv pip install --system /opt/sentinel-proto
COPY apps/analytics-service/pyproject.toml .
RUN uv pip install --system -e ".[dev]"
COPY apps/analytics-service/ .
EXPOSE 5000
CMD ["uvicorn", "sentinel_analytics.main:app", "--host", "0.0.0.0", "--port", "5000"]
