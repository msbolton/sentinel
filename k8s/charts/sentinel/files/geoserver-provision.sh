#!/bin/sh
# =============================================================================
# GeoServer provisioning for SENTINEL
# =============================================================================
# Creates (or updates) the sentinel workspace, a PostGIS store on the
# read-only sentinel_gis role, one layer per clearance level over the
# entities_live_* views plus locations_geo, the entities style, the
# ROLE_CLASS_* roles with one GIS account each, and the layer access rules.
# Every step reads before it writes, so re-running against a persisted data
# directory is a no-op. Runs in the geoserver-init compose service
# (curlimages/curl) and as the Helm post-install job.
#
# Environment:
#   GEOSERVER_URL            http://geoserver:8600/geoserver
#   GEOSERVER_ADMIN_USER     admin
#   GEOSERVER_ADMIN_PASSWORD geoserver_dev
#   GIS_DB_HOST/PORT/NAME    postgres / 5432 / sentinel
#   GIS_DB_USER/PASSWORD     sentinel_gis / sentinel_gis_dev
#   GIS_USER_PASSWORD_U/_C/_S/_TS  passwords of the gis-u … gis-ts accounts
#   STYLE_FILE               path of the SLD (default /scripts/entities.sld)
# =============================================================================
set -eu

GS="${GEOSERVER_URL:-http://geoserver:8600/geoserver}"
AUTH="${GEOSERVER_ADMIN_USER:-admin}:${GEOSERVER_ADMIN_PASSWORD:-geoserver_dev}"
WS="sentinel"
STORE="sentinel-postgis"
STYLE_FILE="${STYLE_FILE:-/scripts/entities.sld}"

DB_HOST="${GIS_DB_HOST:-postgres}"
DB_PORT="${GIS_DB_PORT:-5432}"
DB_NAME="${GIS_DB_NAME:-sentinel}"
DB_USER="${GIS_DB_USER:-sentinel_gis}"
DB_PASS="${GIS_DB_PASSWORD:-sentinel_gis_dev}"

PW_U="${GIS_USER_PASSWORD_U:-gis-u-dev}"
PW_C="${GIS_USER_PASSWORD_C:-gis-c-dev}"
PW_S="${GIS_USER_PASSWORD_S:-gis-s-dev}"
PW_TS="${GIS_USER_PASSWORD_TS:-gis-ts-dev}"

log() { echo "geoserver-init: $*"; }

# status METHOD PATH [content-type] [data-or-@file] -> prints the HTTP status
status() {
  s_method="$1"; s_path="$2"; s_ctype="${3:-application/json}"; s_data="${4:-}"
  if [ -n "$s_data" ]; then
    curl -s -o /tmp/gs-body -w '%{http_code}' -u "$AUTH" -X "$s_method" -H "Content-Type: $s_ctype" --data-binary "$s_data" "$GS$s_path" || echo 000
  else
    curl -s -o /tmp/gs-body -w '%{http_code}' -u "$AUTH" -X "$s_method" -H "Accept: application/json" "$GS$s_path" || echo 000
  fi
}

# ensure NAME GET_PATH POST_PATH PUT_PATH BODY [content-type]
# Creates when GET is 404, updates when it exists, fails on anything else.
ensure() {
  # Plain sh has no locals: these names are used nowhere else.
  e_what="$1"; e_get="$2"; e_post="$3"; e_put="$4"; e_body="$5"; e_ctype="${6:-application/json}"
  code=$(status GET "$e_get")
  case "$code" in
    200) if [ -n "$e_put" ]; then
           code=$(status PUT "$e_put" "$e_ctype" "$e_body")
           case "$code" in 2*) log "updated $e_what";; *) log "FAILED updating $e_what ($code): $(cat /tmp/gs-body)"; exit 1;; esac
         else
           log "exists $e_what"
         fi;;
    404) code=$(status POST "$e_post" "$e_ctype" "$e_body")
         case "$code" in 2*) log "created $e_what";; *) log "FAILED creating $e_what ($code): $(cat /tmp/gs-body)"; exit 1;; esac;;
    *)   log "FAILED reading $e_what ($code): $(cat /tmp/gs-body)"; exit 1;;
  esac
}

# --- wait for GeoServer ------------------------------------------------------
i=0
until [ "$(status GET /rest/about/version.json)" = "200" ]; do
  i=$((i + 1))
  [ "$i" -gt 60 ] && { log "GeoServer did not answer at $GS"; exit 1; }
  sleep 5
done
log "GeoServer is up"

# --- workspace ---------------------------------------------------------------
ensure "workspace $WS" "/rest/workspaces/$WS.json" "/rest/workspaces" "" "{\"workspace\":{\"name\":\"$WS\"}}"

# --- PostGIS store on the read-only role --------------------------------------
store_body=$(cat <<EOF
{"dataStore":{"name":"$STORE","type":"PostGIS","enabled":true,"connectionParameters":{"entry":[
  {"@key":"dbtype","\$":"postgis"},
  {"@key":"host","\$":"$DB_HOST"},
  {"@key":"port","\$":"$DB_PORT"},
  {"@key":"database","\$":"$DB_NAME"},
  {"@key":"schema","\$":"sentinel"},
  {"@key":"user","\$":"$DB_USER"},
  {"@key":"passwd","\$":"$DB_PASS"},
  {"@key":"Expose primary keys","\$":"true"},
  {"@key":"Primary key metadata table","\$":"gt_pk_metadata"},
  {"@key":"Loose bbox","\$":"true"},
  {"@key":"validate connections","\$":"true"}
]}}}
EOF
)
ensure "store $STORE" "/rest/workspaces/$WS/datastores/$STORE.json" "/rest/workspaces/$WS/datastores" "/rest/workspaces/$WS/datastores/$STORE" "$store_body"

# --- style ------------------------------------------------------------------
if [ -f "$STYLE_FILE" ]; then
  code=$(status GET "/rest/workspaces/$WS/styles/entities.json")
  case "$code" in
    200) code=$(status PUT "/rest/workspaces/$WS/styles/entities" "application/vnd.ogc.sld+xml" "@$STYLE_FILE")
         case "$code" in 2*) log "updated style entities";; *) log "FAILED updating style ($code): $(cat /tmp/gs-body)"; exit 1;; esac;;
    404) code=$(status POST "/rest/workspaces/$WS/styles?name=entities" "application/vnd.ogc.sld+xml" "@$STYLE_FILE")
         case "$code" in 2*) log "created style entities";; *) log "FAILED creating style ($code): $(cat /tmp/gs-body)"; exit 1;; esac;;
    *)   log "FAILED reading style ($code)"; exit 1;;
  esac
fi

# --- layers -------------------------------------------------------------------
world='{"minx":-180,"maxx":180,"miny":-90,"maxy":90,"crs":"EPSG:4326"}'
publish() {
  name="$1"; title="$2"; style="$3"
  body="{\"featureType\":{\"name\":\"$name\",\"nativeName\":\"$name\",\"title\":\"$title\",\"srs\":\"EPSG:4326\",\"enabled\":true,\"nativeBoundingBox\":$world,\"latLonBoundingBox\":$world}}"
  ensure "layer $name" "/rest/workspaces/$WS/datastores/$STORE/featuretypes/$name.json" "/rest/workspaces/$WS/datastores/$STORE/featuretypes" "" "$body"
  if [ -n "$style" ]; then
    code=$(status PUT "/rest/layers/$WS:$name" "application/json" "{\"layer\":{\"defaultStyle\":{\"name\":\"$WS:$style\"}}}")
    case "$code" in 2*) ;; *) log "WARN could not set style on $name ($code)";; esac
  fi
}
publish entities_live_u  "Live entities (UNCLASSIFIED)"  entities
publish entities_live_c  "Live entities (CONFIDENTIAL)"  entities
publish entities_live_s  "Live entities (SECRET)"        entities
publish entities_live_ts "Live entities (TOP SECRET)"    entities
publish locations_geo    "Locations"                     ""

# --- roles, accounts and access rules ----------------------------------------
role() {
  r="$1"
  code=$(status GET "/rest/security/roles/role/$r")
  # The roles endpoint answers 200 with a list for GET /roles; a single role is
  # created idempotently with POST (409/500 when it exists on some versions).
  code=$(status POST "/rest/security/roles/role/$r")
  case "$code" in 2*) log "created role $r";; 409|500) log "exists role $r";; *) log "FAILED role $r ($code): $(cat /tmp/gs-body)"; exit 1;; esac
}
account() {
  u="$1"; pw="$2"; r="$3"
  body="{\"user\":{\"userName\":\"$u\",\"password\":\"$pw\",\"enabled\":true}}"
  code=$(status POST "/rest/security/usergroup/users" "application/json" "$body")
  case "$code" in
    2*) log "created user $u";;
    *)  code=$(status POST "/rest/security/usergroup/user/$u" "application/json" "{\"user\":{\"password\":\"$pw\",\"enabled\":true}}")
        case "$code" in 2*) log "updated user $u";; *) log "WARN user $u ($code): $(cat /tmp/gs-body)";; esac;;
  esac
  code=$(status POST "/rest/security/roles/role/$r/user/$u")
  case "$code" in 2*|409|500) ;; *) log "WARN associate $u with $r ($code)";; esac
}
role ROLE_CLASS_U;  role ROLE_CLASS_C;  role ROLE_CLASS_S;  role ROLE_CLASS_TS
account gis-u  "$PW_U"  ROLE_CLASS_U
account gis-c  "$PW_C"  ROLE_CLASS_C
account gis-s  "$PW_S"  ROLE_CLASS_S
account gis-ts "$PW_TS" ROLE_CLASS_TS

acl=$(cat <<EOF
{"$WS.entities_live_u.r":"ROLE_CLASS_U,ROLE_CLASS_C,ROLE_CLASS_S,ROLE_CLASS_TS,ADMIN",
 "$WS.entities_live_c.r":"ROLE_CLASS_C,ROLE_CLASS_S,ROLE_CLASS_TS,ADMIN",
 "$WS.entities_live_s.r":"ROLE_CLASS_S,ROLE_CLASS_TS,ADMIN",
 "$WS.entities_live_ts.r":"ROLE_CLASS_TS,ADMIN",
 "$WS.locations_geo.r":"*",
 "$WS.*.w":"ADMIN"}
EOF
)
code=$(status POST "/rest/security/acl/layers" "application/json" "$acl")
case "$code" in
  2*) log "created layer access rules";;
  *)  code=$(status PUT "/rest/security/acl/layers" "application/json" "$acl")
      case "$code" in 2*) log "updated layer access rules";; *) log "FAILED access rules ($code): $(cat /tmp/gs-body)"; exit 1;; esac;;
esac

# WMS and WFS are enabled globally; a workspace-level service override is
# left alone (a partial one breaks the workspace's GetCapabilities).

log "done: $GS/$WS/wfs?service=WFS&request=GetCapabilities"
