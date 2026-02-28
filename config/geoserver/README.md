# GeoServer Configuration

GeoServer data directory is mounted as a Docker volume.

## Initial Setup

1. Access GeoServer admin at http://localhost:8600/geoserver/web/
2. Default credentials: admin / geoserver
3. Create workspace: `sentinel`
4. Add PostGIS data store pointing to the PostgreSQL instance
5. Publish layers from PostGIS tables

## Layers

- `sentinel:entities` - Entity positions (PostGIS point layer)
- `sentinel:track_points` - Track history (PostGIS point layer)
- `sentinel:aor_polygons` - Areas of Responsibility (PostGIS polygon layer)

## Symbology

MIL-STD-2525D symbology is applied via SLD styles.
Configure SLD files in the styles/ subdirectory.
