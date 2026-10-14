-- =============================================================================
-- SENTINEL development seed data
-- =============================================================================
-- Inserts only. Run after migrations:  make db-seed
-- Idempotent: rows are keyed by name and skipped when they already exist.
-- =============================================================================

-- 13 sample entities across the Middle East region.
-- One statement per row with untyped literals so the values coerce to the
-- column type whether it is VARCHAR (baseline) or a legacy TypeORM enum.
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'VESSEL', 'MV ATLANTIC VOYAGER', 'GEOINT', 'UNCLASSIFIED', ST_SetSRID(ST_MakePoint(53.123, 26.234), 4326), 315, 12.5, 'SFSPCLFF--', ARRAY['COMMERCIAL','FLAGGED_PANAMA'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'MV ATLANTIC VOYAGER');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'VESSEL', 'DHOW-7742', 'SIGINT', 'SECRET', ST_SetSRID(ST_MakePoint(54.321, 25.876), 4326), 180, 6.2, 'SHSPCLFF--', ARRAY['SUSPICIOUS','FLAGGED_UNKNOWN'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'DHOW-7742');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'VESSEL', 'USS MASON (DDG-87)', 'MANUAL', 'UNCLASSIFIED', ST_SetSRID(ST_MakePoint(52.887, 25.551), 4326), 90, 18.0, 'SFSPCLDD--', ARRAY['USN','FIFTH_FLEET'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'USS MASON (DDG-87)');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'AIRCRAFT', 'EAGLE-01', 'SIGINT', 'SECRET', ST_SetSRID(ST_MakePoint(44.366, 33.315), 4326), 270, 420, 'SFAPMFF---', ARRAY['COALITION','ISR'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'EAGLE-01');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'AIRCRAFT', 'UAE-117', 'OSINT', 'UNCLASSIFIED', ST_SetSRID(ST_MakePoint(54.651, 24.433), 4326), 45, 350, 'SFAPMFF---', ARRAY['UAE_AF'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'UAE-117');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'VEHICLE', 'CONVOY-ALPHA', 'GEOINT', 'SECRET', ST_SetSRID(ST_MakePoint(36.789, 35.123), 4326), 135, 25, 'SHGPUCVR--', ARRAY['MILITARY','HOSTILE'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'CONVOY-ALPHA');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'VEHICLE', 'SUPPLY-TRUCK-22', 'HUMINT', 'CONFIDENTIAL', ST_SetSRID(ST_MakePoint(36.891, 35.234), 4326), 90, 35, 'SFGPUCVR--', ARRAY['LOGISTICS','FRIENDLY'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'SUPPLY-TRUCK-22');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'PERSON', 'HVT-ALPHA', 'HUMINT', 'TOP_SECRET', ST_SetSRID(ST_MakePoint(36.292, 33.513), 4326), 0, 0, 'SHGPUCP---', ARRAY['HIGH_VALUE_TARGET'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'HVT-ALPHA');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'PERSON', 'INFORMANT-BRAVO', 'HUMINT', 'SECRET', ST_SetSRID(ST_MakePoint(35.495, 33.888), 4326), 0, 0, 'SFGPUCP---', ARRAY['ASSET','FRIENDLY'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'INFORMANT-BRAVO');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'FACILITY', 'COMPOUND-DELTA', 'GEOINT', 'SECRET', ST_SetSRID(ST_MakePoint(38.123, 34.789), 4326), 0, 0, 'SHGPI-----', ARRAY['HOSTILE','WEAPONS_STORAGE'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'COMPOUND-DELTA');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'FACILITY', 'FOB LIBERTY', 'MANUAL', 'CONFIDENTIAL', ST_SetSRID(ST_MakePoint(44.395, 33.298), 4326), 0, 0, 'SFGPI-----', ARRAY['FRIENDLY','COALITION_BASE'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'FOB LIBERTY');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'SIGNAL', 'EMITTER-7790', 'SIGINT', 'TOP_SECRET', ST_SetSRID(ST_MakePoint(37.456, 35.678), 4326), 0, 0, 'SHSPS-----', ARRAY['RADAR','AIR_DEFENSE'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = 'EMITTER-7790');
INSERT INTO sentinel.entities ("entityType", name, source, classification, position, heading, "speedKnots", "milStd2525dSymbol", affiliations, "lastSeenAt")
SELECT 'UNIT', '3RD BDE, 1ST AD', 'MANUAL', 'CONFIDENTIAL', ST_SetSRID(ST_MakePoint(44.789, 32.456), 4326), 0, 0, 'SFGPUCA---', ARRAY['US_ARMY','1ST_ARMORED_DIV'], NOW()
WHERE NOT EXISTS (SELECT 1 FROM sentinel.entities WHERE name = '3RD BDE, 1ST AD');

-- Sample geofence rule covering the Persian Gulf.
INSERT INTO sentinel.alert_rules (name, "ruleType", config, "monitoredEntityTypes", severity)
SELECT 'Persian Gulf Exclusion Zone', 'GEOFENCE_BREACH',
       '{"polygon": [{"latitude": 27.0, "longitude": 51.0}, {"latitude": 27.0, "longitude": 56.0}, {"latitude": 24.0, "longitude": 56.0}, {"latitude": 24.0, "longitude": 51.0}], "trigger_on_enter": true, "trigger_on_exit": false}'::jsonb,
       ARRAY['VESSEL','AIRCRAFT'], 'HIGH'
WHERE NOT EXISTS (SELECT 1 FROM sentinel.alert_rules WHERE name = 'Persian Gulf Exclusion Zone');

-- Saved camera locations.
INSERT INTO sentinel.locations (name, description, latitude, longitude, altitude, heading, pitch, "range", "has3dTiles", category)
SELECT v.name, v.description, v.lat, v.lng, v.altitude, v.heading, v.pitch, v.range, v.has3d, v.category
FROM (VALUES
  ('New York City',         'Financial capital, largest US city',                                    40.7128::double precision, -74.0060::double precision, 1500::double precision, 0::double precision, -45::double precision, 3000::double precision, TRUE,  'CITY'),
  ('London',                'Capital of the United Kingdom',                                         51.5074, -0.1278,   1500, 0, -45, 3000, TRUE,  'CITY'),
  ('Dubai',                 'Major city in the United Arab Emirates',                                25.2048, 55.2708,   1500, 0, -45, 3000, TRUE,  'CITY'),
  ('Washington DC',         'Capital of the United States',                                          38.9072, -77.0369,  1200, 0, -45, 2500, TRUE,  'CITY'),
  ('Tokyo',                 'Capital of Japan',                                                      35.6762, 139.6503,  1500, 0, -45, 3000, TRUE,  'CITY'),
  ('Sydney',                'Largest city in Australia',                                            -33.8688, 151.2093,  1500, 0, -45, 3000, TRUE,  'CITY'),
  ('Singapore',             'City-state in Southeast Asia',                                           1.3521, 103.8198,  1200, 0, -45, 2500, TRUE,  'CITY'),
  ('Paris',                 'Capital of France',                                                     48.8566, 2.3522,    1200, 0, -45, 2500, TRUE,  'CITY'),
  ('Naval Station Norfolk', 'Largest naval base in the world, home of US Atlantic Fleet',            36.9461, -76.3013,   800, 0, -35, 2000, FALSE, 'MILITARY_BASE'),
  ('Pearl Harbor',          'Joint Base Pearl Harbor-Hickam, headquarters of US Pacific Fleet',      21.3500, -157.9500,  800, 0, -35, 2000, FALSE, 'MILITARY_BASE')
) AS v(name, description, lat, lng, altitude, heading, pitch, range, has3d, category)
WHERE NOT EXISTS (SELECT 1 FROM sentinel.locations l WHERE l.name = v.name);
