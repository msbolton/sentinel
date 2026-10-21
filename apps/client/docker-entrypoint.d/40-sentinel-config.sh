#!/bin/sh
# Writes the runtime config the web client loads from /config.json before it starts.
#
# The production bundle carries no map keys; they come from the container's
# environment instead, so one image serves every deployment. Values are written
# into JSON without escaping, so only [A-Za-z0-9._-] is accepted (Google API keys
# and Cesium Ion JWTs fit). Anything else is a misconfiguration and stops the
# container rather than risking a broken or injected config.
set -eu
export LC_ALL=C

config_dir="${SENTINEL_CONFIG_DIR:-/var/run/sentinel}"
config_file="$config_dir/config.json"

google_maps_api_key="${GOOGLE_MAPS_API_KEY:-}"
cesium_ion_access_token="${CESIUM_ION_ACCESS_TOKEN:-}"

check() {
    case "$2" in
        *[!A-Za-z0-9._-]*)
            echo "40-sentinel-config: $1 contains characters outside [A-Za-z0-9._-]; refusing to start" >&2
            exit 1
            ;;
    esac
}

check GOOGLE_MAPS_API_KEY "$google_maps_api_key"
check CESIUM_ION_ACCESS_TOKEN "$cesium_ion_access_token"

mkdir -p "$config_dir"
tmp="$config_dir/.config.json.$$"
printf '{"googleMapsApiKey":"%s","cesiumIonAccessToken":"%s"}\n' \
    "$google_maps_api_key" "$cesium_ion_access_token" > "$tmp"
mv "$tmp" "$config_file"

state() { if [ -n "$1" ]; then echo set; else echo empty; fi; }
echo "40-sentinel-config: wrote $config_file (googleMapsApiKey: $(state "$google_maps_api_key"), cesiumIonAccessToken: $(state "$cesium_ion_access_token"))"
