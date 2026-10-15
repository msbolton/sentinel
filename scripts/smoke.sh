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
check GET "/api/v1/links/graph?centerId=00000000-0000-4000-8000-000000000000" 200
check GET "/api/v1/search?q=test" 200
check GET /api/v1/search/facets 200
check GET "/api/v1/tracks/timeline?buckets=4" 200
check GET /api/v1/feeds 200
check GET /api/v1/locations 200
check POST /api/v1/entities 400 '{}'
check GET /api/v1/locations/not-a-uuid 400

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
