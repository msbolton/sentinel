#!/usr/bin/env bash
# =============================================================================
# SENTINEL smoke test
# =============================================================================
# Exercises every API gateway route the client depends on against a running
# stack (docker compose up, or local services behind the gateway on :3000).
# Usage: scripts/smoke.sh [GATEWAY_URL] [CLIENT_URL]
# Exit code is non-zero when any check fails.
# =============================================================================
set -u
GW="${1:-http://localhost:3000}"
CLIENT="${2:-http://localhost:4200}"
TOKEN="${SENTINEL_TOKEN:-}"   # optional bearer token when AUTH_DEV_BYPASS is off
fail=0

# bash 3.2 (macOS) treats an empty array as unset under `set -u`; expand with a default.
auth=()
[ -n "$TOKEN" ] && auth=(-H "Authorization: Bearer $TOKEN")

check() { # method path expected-status [json-body]
  local method=$1 path=$2 want=$3 body=${4:-}
  local args=(-s -o /tmp/smoke-body -w '%{http_code}' -X "$method" ${auth[@]+"${auth[@]}"})
  [ -n "$body" ] && args+=(-H 'Content-Type: application/json' -d "$body")
  local code
  code=$(curl "${args[@]}" "$GW$path" 2>/dev/null)
  if [ "$code" = "$want" ]; then
    printf '  ok   %-6s %-55s %s\n' "$method" "$path" "$code"
  else
    printf '  FAIL %-6s %-55s %s (want %s) %s\n' "$method" "$path" "$code" "$want" "$(head -c 160 /tmp/smoke-body 2>/dev/null)"
    fail=1
  fi
}

echo "== gateway: $GW"
check GET /api/v1/health 200
check GET /api/v1/health/ready 200
check GET "/api/v1/entities?pageSize=1" 200
check GET /api/v1/entities/counts 200
check GET /api/v1/entities/ageout-config 200
check GET "/api/v1/alerts?pageSize=1" 200
check GET /api/v1/alerts/rules 200
check GET /api/v1/analytics/stats 200
check GET "/api/v1/links/graph?centerId=00000000-0000-4000-8000-000000000000" 200
check GET "/api/v1/search?q=test" 200
check GET /api/v1/search/facets 200
check GET "/api/v1/tracks/timeline?buckets=4" 200
check GET /api/v1/feeds 200
check GET /api/v1/feeds/opensky 200
check GET /api/v1/feeds/opensky/status 200
check GET /api/v1/feeds/opensky/area 200
check GET /api/v1/drivers 200
check GET /api/v1/drivers/stream 200
check GET /api/v1/drivers/nope 404
check GET /api/v1/drivers/celestrak 200
grep -q '"catalogNumbers"' /tmp/smoke-body && echo "  ok   celestrak schema lists catalogNumbers" || { echo "  FAIL celestrak schema: $(head -c 120 /tmp/smoke-body)"; fail=1; }
check GET /api/v1/drivers/intelsky 200
grep -q '"intervalSec"' /tmp/smoke-body && echo "  ok   intelsky schema lists intervalSec" || { echo "  FAIL intelsky schema: $(head -c 120 /tmp/smoke-body)"; fail=1; }
check GET /api/v1/drivers/adsblol 404
check GET /api/v1/aircraft/zzz/path 400
check GET /api/v1/drivers/aisstream 200
grep -q '"watchlistMmsi"' /tmp/smoke-body && echo "  ok   aisstream schema lists watchlistMmsi" || { echo "  FAIL aisstream schema: $(head -c 120 /tmp/smoke-body)"; fail=1; }
check GET /api/v1/feeds/aisstream 200
# The feed holds a reference to the key, never the key itself.
grep -q '"apiKey":"env:AISSTREAM_API_KEY"' /tmp/smoke-body && echo "  ok   aisstream key is a reference" || { echo "  FAIL aisstream secretRefs: $(grep -o '"secretRefs":{[^}]*}' /tmp/smoke-body)"; fail=1; }
check GET /api/v1/locations 200
check POST /api/v1/entities 400 '{}'
check GET /api/v1/locations/not-a-uuid 400

echo "== observations"
eid=$(curl -s ${auth[@]+"${auth[@]}"} "$GW/api/v1/entities?pageSize=1" | sed -nE 's/.*"id":"([0-9a-f-]{36})".*/\1/p' | head -1)
if [ -n "$eid" ]; then
  check GET "/api/v1/entities/observations/$eid?limit=5" 200
else
  echo "  skip GET    /api/v1/entities/observations/{id} (no entities)"
fi

echo "== feeds"
check POST /api/v1/feeds 422 '{"name":"Smoke bad feed","driver":"stream","config":{"decoder":{"name":"sbs"}}}'
check DELETE /api/v1/feeds/opensky 403
if [ -n "${SENTINEL_VIEWER_TOKEN:-}" ]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $SENTINEL_VIEWER_TOKEN" "$GW/api/v1/feeds/opensky/enable" 2>/dev/null)
  [ "$code" = "403" ] && echo "  ok   POST   /api/v1/feeds/opensky/enable as viewer      403" || { echo "  FAIL POST   /api/v1/feeds/opensky/enable as viewer -> $code (want 403)"; fail=1; }
else
  echo "  skip viewer 403 on feed toggle (set SENTINEL_VIEWER_TOKEN)"
fi

echo "== connections"
check GET /api/v1/drivers/tak 200
grep -q '"direction":"both"' /tmp/smoke-body && echo "  ok   tak driver direction both" || { echo "  FAIL tak driver direction missing"; fail=1; }
check POST /api/v1/feeds 422 '{"name":"Smoke bad policy","driver":"tak","config":{"address":"x:8089","policy":{"boxes":[{"north":10,"south":20,"east":0,"west":0}]}}}'
check POST /api/v1/feeds 422 '{"name":"Smoke bad ceiling","driver":"tak","config":{"address":"x:8089","policy":{"classificationCeiling":"ULTRA"}}}'
check POST /api/v1/feeds 422 '{"name":"Smoke policy on a source","driver":"opensky","config":{"policy":{"entityTypes":["AIRCRAFT"]}}}'
check GET "/api/v1/feeds?direction=out" 200

echo "== inbound tcp/udp"
check GET /api/v1/drivers/tcp 200
grep -q '"x-showWhen"' /tmp/smoke-body && echo "  ok   tcp schema has conditional fields" || { echo "  FAIL tcp schema: $(head -c 120 /tmp/smoke-body)"; fail=1; }
check GET /api/v1/drivers/udp 200
# The management API answers validation problems with 422; a low port and one of Sentinel's own are refused.
check POST /api/v1/feeds 422 '{"name":"smoke low port","driver":"udp","config":{"port":80}}'
check POST /api/v1/feeds 422 '{"name":"smoke reserved port","driver":"udp","config":{"port":5432}}'

echo "== export"
check GET "/api/v1/export/entities.geojson?limit=1" 200
grep -q '"type":"FeatureCollection"' /tmp/smoke-body && echo "  ok   geojson FeatureCollection" || { echo "  FAIL geojson body: $(head -c 120 /tmp/smoke-body)"; fail=1; }
check GET "/api/v1/export/entities.kml?limit=1" 200
grep -q '<kml' /tmp/smoke-body && echo "  ok   kml document" || { echo "  FAIL kml body: $(head -c 120 /tmp/smoke-body)"; fail=1; }
check GET "/api/v1/export/entities.kml?link=1&refresh=30" 200
grep -q '<NetworkLink>' /tmp/smoke-body && echo "  ok   kml NetworkLink" || { echo "  FAIL kml network link: $(head -c 120 /tmp/smoke-body)"; fail=1; }
check GET "/api/v1/export/entities.geojson?bbox=1,2,3" 400
check GET /api/v1/auth/api-keys 200

echo "== analytics"
check GET "/api/v1/analytics/findings?status=OPEN&pageSize=1" 200
grep -q '"data"' /tmp/smoke-body && echo "  ok   findings page" || { echo "  FAIL findings body: $(head -c 120 /tmp/smoke-body)"; fail=1; }
check GET /api/v1/analytics/entities/00000000-0000-4000-8000-000000000000/baseline 404
check GET /api/v1/analytics/findings/00000000-0000-4000-8000-000000000000 404
check GET /api/v1/analytics/stats 200

if command -v docker >/dev/null 2>&1 && docker ps --format '{{.Names}}' 2>/dev/null | grep -qx sentinel-geoserver; then
  echo "== geoserver"
  caps=$(curl -s -u "${GIS_USER:-gis-ts}:${GIS_PASSWORD:-gis-ts-dev}" "http://localhost:8600/geoserver/sentinel/wfs?service=WFS&version=2.0.0&request=GetCapabilities" 2>/dev/null)
  for layer in entities_live_u locations_geo; do
    if printf '%s' "$caps" | grep -q "sentinel:$layer"; then echo "  ok   wfs    sentinel:$layer"; else echo "  FAIL wfs    sentinel:$layer missing (run docker compose up geoserver-init)"; fail=1; fi
  done
fi

if command -v docker >/dev/null 2>&1 && docker ps --format '{{.Names}}' 2>/dev/null | grep -qx sentinel-kafka; then
  echo "== kafka topics"
  topics=$(docker exec sentinel-kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --list 2>/dev/null)
  for t in observations.raw observations.dlq entities.events alerts.events; do
    if printf '%s\n' "$topics" | grep -qx "$t"; then echo "  ok   topic  $t"; else echo "  FAIL topic  $t missing"; fail=1; fi
  done
fi

echo "== location round trip"
new=$(curl -s ${auth[@]+"${auth[@]}"} -X POST -H 'Content-Type: application/json' -d '{"name":"Smoke Test","latitude":1,"longitude":2}' "$GW/api/v1/locations")
id=$(printf '%s' "$new" | sed -nE 's/.*"id":"([0-9a-f-]{36})".*/\1/p')
if [ -n "$id" ]; then
  check PATCH "/api/v1/locations/$id" 200 '{"name":"Smoke Test 2"}'
  check DELETE "/api/v1/locations/$id" 204
else
  echo "  FAIL POST   /api/v1/locations -> $(printf '%s' "$new" | head -c 160)"; fail=1
fi

echo "== client: $CLIENT"
code=$(curl -s -o /dev/null -w '%{http_code}' "$CLIENT/" 2>/dev/null)
[ "$code" = "200" ] && echo "  ok   GET    /                        $code" || { echo "  FAIL GET    /  -> $code"; fail=1; }
code=$(curl -s -o /dev/null -w '%{http_code}' "$CLIENT/api/v1/health" 2>/dev/null)
[ "$code" = "200" ] && echo "  ok   GET    /api/v1/health (via nginx) $code" || { echo "  FAIL GET    /api/v1/health via nginx -> $code"; fail=1; }

if command -v node >/dev/null 2>&1; then
  echo "== websocket"
  wsurl="${GW/http/ws}/ws/entities"; [ -n "$TOKEN" ] && wsurl="$wsurl?token=$TOKEN"
  if node -e "
    const ws = new WebSocket(process.argv[1]);
    const t = setTimeout(() => { console.log('  FAIL no connection:ack within 5s'); process.exit(1); }, 5000);
    ws.onmessage = (e) => { const f = JSON.parse(e.data); if (f.type === 'connection:ack') { console.log('  ok   connection:ack', f.data.clientId); clearTimeout(t); ws.close(); process.exit(0); } };
    ws.onerror = (e) => { console.log('  FAIL', e.message || 'websocket error'); process.exit(1); };
  " "$wsurl"; then :; else fail=1; fi
fi

[ $fail -eq 0 ] && echo "SMOKE PASSED" || echo "SMOKE FAILED"
exit $fail
