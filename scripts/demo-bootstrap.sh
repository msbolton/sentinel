#!/usr/bin/env bash
# =============================================================================
# SENTINEL public demo bootstrap (docs/demo.md)
# =============================================================================
# Makes sure the demo's two grpc connections exist and are enabled, and that
# each has an ingest token stored in the Secret the chart reads:
#
#   demo-sensors   port 6201  exercise, military, sender-managed emplacements
#                  -> Secret sentinel-demo-sim-token      (sensorSim.tokenSecret)
#   demo-visitors  port 6202  exercise, sender-managed emplacements, 32 sites
#                  -> Secret sentinel-demo-visitors-token (apiGateway.demo.tokenSecret)
#
# Idempotent: a second run reports what exists and changes nothing. It never
# prints a token or the password, and keeps both out of every command line:
# the password reaches curl on stdin, the bearer token through a curl config
# file and the ingest token through a file kubectl reads, each 0600 in a
# private temporary directory that is removed on exit.
#
# Needs bash 3.2 or later, curl and jq, and kubectl unless --no-kubectl.
# =============================================================================
set -euo pipefail

PROG=demo-bootstrap
REALM=sentinel
CLIENT_ID=sentinel-web
SIM_SECRET=sentinel-demo-sim-token
VISITORS_SECRET=sentinel-demo-visitors-token
TOKEN_PREFIX="demo-bootstrap "   # the names of the tokens this script issues

usage() {
  cat <<'EOF'
Usage: scripts/demo-bootstrap.sh --host URL [options]

Creates or checks the public demo's grpc connections (demo-sensors on 6201,
demo-visitors on 6202) and the Secrets holding their ingest tokens
(sentinel-demo-sim-token, sentinel-demo-visitors-token, key "token").

Options:
  --host URL          The site, e.g. https://sentinel.msbolton.dev. The API is
                      URL/api/v1 and Keycloak URL/auth.
  --admin-user NAME   Keycloak user with sentinel-admin or sentinel-operator
                      (default sentinel-admin). The password comes from
                      SENTINEL_ADMIN_PASSWORD, else a prompt that does not echo.
  --namespace NS      Namespace of the release and the Secrets (default sentinel).
  --rotate            Issue new tokens, replace both Secrets, then revoke every
                      other token of the two connections. Restart the gateway
                      and the simulators afterwards (the script prints how).
  --no-kubectl        Do not run kubectl: ensure the connections only, and print
                      the Secrets that would be created. No token is issued,
                      since nothing could store it.
  --dry-run           Sign in and read, but change nothing: print each call
                      that would change something, secrets redacted.
  -h, --help          This help.

Examples:
  scripts/demo-bootstrap.sh --host https://sentinel.msbolton.dev
  SENTINEL_ADMIN_PASSWORD=... scripts/demo-bootstrap.sh --host http://localhost:4200 --admin-user admin --no-kubectl
EOF
}

die() { printf '%s: %s\n' "$PROG" "$*" >&2; exit 1; }
say() { printf '%s\n' "$*"; }

HOST=""
ADMIN_USER=sentinel-admin
NAMESPACE=sentinel
ROTATE=false
USE_KUBECTL=true
DRY_RUN=false

while [ $# -gt 0 ]; do
  case $1 in
    --host) [ $# -ge 2 ] || die "--host needs a URL"; HOST=$2; shift 2 ;;
    --host=*) HOST=${1#*=}; shift ;;
    --admin-user) [ $# -ge 2 ] || die "--admin-user needs a name"; ADMIN_USER=$2; shift 2 ;;
    --admin-user=*) ADMIN_USER=${1#*=}; shift ;;
    --namespace) [ $# -ge 2 ] || die "--namespace needs a name"; NAMESPACE=$2; shift 2 ;;
    --namespace=*) NAMESPACE=${1#*=}; shift ;;
    --rotate) ROTATE=true; shift ;;
    --no-kubectl) USE_KUBECTL=false; shift ;;
    --dry-run) DRY_RUN=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown argument: $1" ;;
  esac
done

[ -n "$HOST" ] || { usage >&2; die "--host is required"; }
HOST=${HOST%/}
# Plain http only to this machine: the host part must be exactly localhost or
# 127.0.0.1 (so not http://localhost:x@elsewhere), with an optional port.
local_http='^http://(localhost|127\.0\.0\.1)(:[0-9]+)?(/|$)'
case $HOST in
  https://*) ;;
  http://*)
    [[ $HOST =~ $local_http ]] \
      || die "refusing to send the admin password over plain http to $HOST (use https, or http://localhost[:port])" ;;
  *) die "--host must start with https:// (or http://localhost)" ;;
esac
[ -n "$ADMIN_USER" ] || die "--admin-user is empty"
[ -n "$NAMESPACE" ] || die "--namespace is empty"
if $ROTATE && ! $USE_KUBECTL; then
  die "--rotate needs kubectl to store the new tokens; drop --no-kubectl"
fi

for tool in curl jq; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done
if $USE_KUBECTL; then
  command -v kubectl >/dev/null 2>&1 || die "kubectl is required (or pass --no-kubectl)"
fi

# Everything secret that touches the disk lives here, readable by us only.
umask 077
tmp_root=${TMPDIR:-/tmp}
WORK=$(mktemp -d "${tmp_root%/}/demo-bootstrap.XXXXXX")
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT
trap 'exit 130' INT TERM

AUTH_CFG=$WORK/auth.curlrc    # curl config holding the Authorization header
BODY=$WORK/body               # the last response body
REQ=$WORK/request.json        # the last request body (never secret)
TOKEN_FILE=$WORK/token        # an issued ingest token, until kubectl has read it

CHANGED_SECRETS=""

# Before anything changes: the cluster answers and the release's namespace exists.
if $USE_KUBECTL; then
  kubectl get namespace "$NAMESPACE" -o name >/dev/null 2>"$WORK/kubectl.err" \
    || die "kubectl get namespace $NAMESPACE: $(tail -n 1 "$WORK/kubectl.err") (check the context, or pass --namespace or --no-kubectl)"
fi

# --- Sign in ------------------------------------------------------------------

if [ -n "${SENTINEL_ADMIN_PASSWORD:-}" ]; then
  password=$SENTINEL_ADMIN_PASSWORD
else
  { exec 3<>/dev/tty; } 2>/dev/null || die "set SENTINEL_ADMIN_PASSWORD (no terminal to prompt on)"
  printf 'Password for %s: ' "$ADMIN_USER" >&3
  IFS= read -rs password <&3 || true
  printf '\n' >&3
  exec 3>&-
fi
[ -n "$password" ] || die "the password is empty"

TOKEN_URL=$HOST/auth/realms/$REALM/protocol/openid-connect/token
$DRY_RUN && say "dry-run: POST $TOKEN_URL grant_type=password client_id=$CLIENT_ID username=$ADMIN_USER password=<redacted> (read-only sign-in)"

# The password goes to curl on stdin (password@-), never on its command line.
status=$(printf '%s' "$password" | curl -sS -o "$BODY" -w '%{http_code}' -X POST "$TOKEN_URL" \
  --data-urlencode grant_type=password --data-urlencode "client_id=$CLIENT_ID" \
  --data-urlencode "username=$ADMIN_USER" --data-urlencode 'password@-') \
  || die "cannot reach $TOKEN_URL"
unset password SENTINEL_ADMIN_PASSWORD
if [ "$status" != 200 ]; then
  reason=$(jq -r '.error_description // .error // empty' "$BODY" 2>/dev/null || true)
  die "sign-in as $ADMIN_USER failed: HTTP $status${reason:+ ($reason)}"
fi
# The access token goes straight from the response into the curl config file.
jq -er '.access_token | select(type == "string" and length > 0) | "header = \"Authorization: Bearer " + . + "\""' \
  "$BODY" >"$AUTH_CFG" 2>/dev/null || die "the sign-in response has no access token"
: >"$BODY"
say "signed in as $ADMIN_USER at $HOST"

# --- HTTP helpers -------------------------------------------------------------

# try_api METHOD PATH [BODY_FILE]: one gateway call. Sets STATUS (000 when the
# gateway cannot be reached); the body is in $BODY.
try_api() {
  local method=$1 path=$2 data=${3:-}
  local args=(-sS -K "$AUTH_CFG" -o "$BODY" -w '%{http_code}' -X "$method" -H 'Accept: application/json')
  if [ -n "$data" ]; then
    args+=(-H 'Content-Type: application/json' --data-binary "@$data")
  fi
  STATUS=$(curl "${args[@]}" "$HOST$path") || STATUS=000
}

# api METHOD PATH [BODY_FILE]: try_api, dying when the gateway cannot be reached.
api() {
  try_api "$@"
  [ "$STATUS" != 000 ] || die "cannot reach $HOST$2"
}

# The error of the last response, without the body's other fields.
api_error() {
  jq -r '[.error // .message // empty] + [(.details // [])[] | "\(.path): \(.message)"] | join("; ")' "$BODY" 2>/dev/null || true
}

# expect WANT DOING: dies unless the last call answered WANT.
expect() {
  local want=$1 doing=$2 reason
  [ "$STATUS" = "$want" ] && return 0
  reason=$(api_error)
  die "$doing failed: HTTP $STATUS${reason:+ ($reason)}"
}

# would METHOD PATH [BODY_FILE]: prints a call that a dry run does not make.
would() {
  local body=""
  if [ -n "${3:-}" ]; then body=" $(jq -c . "$3")"; fi
  say "dry-run: would $1 $HOST$2$body"
}

# --- Secrets ------------------------------------------------------------------

# secret_exists NAME: 0 if the Secret exists, 1 if not; dies on any other error.
secret_exists() {
  if kubectl -n "$NAMESPACE" get secret "$1" -o name >/dev/null 2>"$WORK/kubectl.err"; then
    return 0
  fi
  if grep -q 'NotFound' "$WORK/kubectl.err"; then
    return 1
  fi
  die "kubectl -n $NAMESPACE get secret $1: $(tail -n 1 "$WORK/kubectl.err")"
}

# active_tokens FEED [EXCEPT_ID [NAME_PREFIX]]: ids of the feed's unrevoked
# tokens (an expired one counts too: revoking it does no harm), one per line,
# but EXCEPT_ID and, with NAME_PREFIX, only those whose name starts with it.
active_tokens() {
  api GET "/api/v1/feeds/$1/tokens"
  expect 200 "listing the tokens of $1"
  jq -r --arg except "${2:-}" --arg prefix "${3:-}" \
    '.[] | select(.revokedAt == null and .id != $except and (.name | startswith($prefix))) | .id' "$BODY"
}

# revoke_others FEED KEEP_ID [NAME_PREFIX]: revokes the tokens active_tokens lists.
revoke_others() {
  local feed=$1 keep=$2 prefix=${3:-} ids id
  ids=$(active_tokens "$feed" "$keep" "$prefix")
  for id in $ids; do
    if $DRY_RUN; then
      would DELETE "/api/v1/feeds/$feed/tokens/$id"
      continue
    fi
    api DELETE "/api/v1/feeds/$feed/tokens/$id"
    expect 204 "revoking token $id of $feed"
    say "  revoked token $id of $feed"
  done
}

# revoke_unstored FEED TOKEN_ID TOKEN_NAME REASON: nothing holds a token just
# issued, so revoke it rather than leave it live, then die with REASON and
# what became of the token.
revoke_unstored() {
  local feed=$1 id=$2 name=$3 reason=$4 why
  try_api DELETE "/api/v1/feeds/$feed/tokens/$id"
  if [ "$STATUS" = 204 ]; then
    die "$reason; the new token $id was revoked again"
  fi
  why=$(api_error)
  die "$reason, and revoking the new token $id failed (HTTP $STATUS${why:+, $why}): revoke \"$name\" of $feed in Connections"
}

# ensure_secret SECRET FEED FEED_IS_NEW
ensure_secret() {
  local secret=$1 feed=$2 feed_new=$3 exists=false token_id token_name

  if ! $USE_KUBECTL; then
    say "Secret $secret (key token) for $feed: not checked (--no-kubectl); would create it from a new token of $feed"
    return 0
  fi
  if secret_exists "$secret"; then exists=true; fi

  if $exists && ! $ROTATE; then
    # A Secret is only as good as the connection's tokens: with none unrevoked
    # (the connection was just created, or its tokens were revoked by hand) the
    # Secret's token cannot work, so it is replaced.
    local ids count=0 id
    if ! $feed_new; then
      ids=$(active_tokens "$feed")
      for id in $ids; do count=$((count + 1)); done
    fi
    if [ "$count" -gt 0 ]; then
      say "Secret $secret exists; its token is left as is"
      [ "$count" -eq 1 ] || say "  note: $feed has $count unrevoked tokens; --rotate replaces them with one new one"
      return 0
    fi
    say "Secret $secret exists, but $feed has no unrevoked token, so the Secret's is stale: replacing it"
  fi

  token_name="$TOKEN_PREFIX$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  jq -n --arg name "$token_name" '{name: $name}' >"$REQ"
  if $DRY_RUN; then
    would POST "/api/v1/feeds/$feed/tokens" "$REQ"
    if $exists; then
      say "dry-run: would run kubectl -n $NAMESPACE create secret generic $secret --from-file=token=<0600 temp file> --dry-run=client -o yaml | kubectl -n $NAMESPACE replace -f -"
    else
      say "dry-run: would run kubectl -n $NAMESPACE create secret generic $secret --from-file=token=<0600 temp file>"
    fi
    if $ROTATE && ! $feed_new; then
      revoke_others "$feed" ""
    elif ! $exists && ! $feed_new; then
      revoke_others "$feed" "" "$TOKEN_PREFIX"
    fi
    return 0
  fi

  api POST "/api/v1/feeds/$feed/tokens" "$REQ"
  expect 201 "issuing a token for $feed"
  # The secret goes from the response to a 0600 file, never to a variable or
  # argv; jq -j writes it with no newline, so the Secret holds the bare token.
  token_id=$(jq -r '.id // empty' "$BODY" 2>/dev/null || true)
  if [ -z "$token_id" ]; then
    : >"$BODY"
    die "the token response for $feed names no token id, so the new token cannot be revoked from here: revoke \"$token_name\" of $feed in Connections"
  fi
  if ! jq -ej '.token | select(type == "string" and startswith("si_"))' "$BODY" >"$TOKEN_FILE" 2>/dev/null; then
    : >"$BODY"
    rm -f "$TOKEN_FILE"
    revoke_unstored "$feed" "$token_id" "$token_name" "the token response for $feed has no si_ token"
  fi
  : >"$BODY"

  local stored=true
  if $exists; then
    kubectl -n "$NAMESPACE" create secret generic "$secret" --from-file=token="$TOKEN_FILE" --dry-run=client -o yaml \
      | kubectl -n "$NAMESPACE" replace -f - >/dev/null || stored=false
  else
    kubectl -n "$NAMESPACE" create secret generic "$secret" --from-file=token="$TOKEN_FILE" >/dev/null || stored=false
  fi
  rm -f "$TOKEN_FILE"
  if ! $stored; then
    revoke_unstored "$feed" "$token_id" "$token_name" "storing the token of $feed in Secret $secret failed"
  fi
  if $exists; then
    say "Secret $secret replaced: new token \"$token_name\" ($token_id) of $feed"
  else
    say "Secret $secret created: new token \"$token_name\" ($token_id) of $feed"
  fi
  CHANGED_SECRETS="$CHANGED_SECRETS $secret"
  if $ROTATE; then
    revoke_others "$feed" "$token_id"
  elif ! $exists && ! $feed_new; then
    # The missing Secret was the only copy of this script's earlier tokens.
    revoke_others "$feed" "$token_id" "$TOKEN_PREFIX"
  fi
}

# --- Connections --------------------------------------------------------------

# ensure_connection ID NAME DESCRIPTION CONFIG_JSON: creates the connection, or
# brings an existing one to CONFIG_JSON's settings and enables it. Sets FEED_NEW.
ensure_connection() {
  local id=$1 name=$2 description=$3 config=$4 driver drift enabled
  FEED_NEW=false

  api GET "/api/v1/feeds/$id"
  case $STATUS in
    404)
      jq -n --arg id "$id" --arg name "$name" --arg description "$description" --argjson config "$config" \
        '{id: $id, name: $name, description: $description, driver: "grpc", enabled: true, config: $config}' >"$REQ"
      FEED_NEW=true
      if $DRY_RUN; then
        would POST /api/v1/feeds "$REQ"
        return 0
      fi
      api POST /api/v1/feeds "$REQ"
      expect 201 "creating connection $id"
      say "connection $id created (port $(jq -r .port <<<"$config"), enabled)"
      return 0
      ;;
    200) ;;
    *) expect 200 "reading connection $id" ;;
  esac

  driver=$(jq -r .driver "$BODY")
  [ "$driver" = grpc ] || die "connection $id exists with driver $driver, not grpc: delete it or pick another id"
  enabled=$(jq -r .enabled "$BODY")
  # The settings of CONFIG_JSON that the connection does not have (PATCH merges config).
  drift=$(jq -r --argjson want "$config" '.config as $have | [$want | to_entries[] | select($have[.key] != .value) | .key] | join(",")' "$BODY")

  if [ -z "$drift" ] && [ "$enabled" = true ]; then
    say "connection $id exists, enabled, settings match"
    return 0
  fi
  if [ -n "$drift" ]; then
    jq -n --argjson config "$config" '{config: $config}' >"$REQ"
    if $DRY_RUN; then
      would PATCH "/api/v1/feeds/$id" "$REQ"
    else
      api PATCH "/api/v1/feeds/$id" "$REQ"
      expect 200 "updating connection $id"
      say "connection $id exists; updated $drift"
    fi
  fi
  if [ "$enabled" != true ]; then
    if $DRY_RUN; then
      would POST "/api/v1/feeds/$id/enable"
    else
      api POST "/api/v1/feeds/$id/enable"
      expect 200 "enabling connection $id"
      say "connection $id exists; enabled it"
    fi
  fi
}

SENSORS_CONFIG='{"port":6201,"exercise":true,"military":true,"senderManagedEmplacements":true}'
VISITORS_CONFIG='{"port":6202,"exercise":true,"senderManagedEmplacements":true,"maxEmplacements":32}'

ensure_connection demo-sensors "Demo sensors" \
  "The public demo's simulated radar, EO/IR and ESM (sensor-sim in the chart)" "$SENSORS_CONFIG"
ensure_secret "$SIM_SECRET" demo-sensors "$FEED_NEW"

ensure_connection demo-visitors "Demo visitors" \
  "One site per demo guest, sent by the gateway (DEMO_ENABLED)" "$VISITORS_CONFIG"
ensure_secret "$VISITORS_SECRET" demo-visitors "$FEED_NEW"

if [ -n "$CHANGED_SECRETS" ]; then
  restart=""
  case " $CHANGED_SECRETS " in *" $SIM_SECRET "*)
    restart="$restart deploy/sentinel-sensor-sim-radar deploy/sentinel-sensor-sim-eoir deploy/sentinel-sensor-sim-esm" ;;
  esac
  case " $CHANGED_SECRETS " in *" $VISITORS_SECRET "*) restart="$restart deploy/sentinel-api-gateway" ;; esac
  say ""
  say "The pods read their token at start. Restart them (names for the release sentinel):"
  say "  kubectl -n $NAMESPACE rollout restart$restart"
fi
if ! $USE_KUBECTL; then
  say ""
  say "--no-kubectl: Secrets not checked or created: $SIM_SECRET, $VISITORS_SECRET"
fi
if $DRY_RUN; then
  say "dry-run: nothing was changed"
fi
