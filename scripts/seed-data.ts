/**
 * Seed script to populate SENTINEL with sample entities for development.
 * Usage: npx ts-node scripts/seed-data.ts
 */

import { Client } from 'pg';

interface SeedEntity {
  name: string;
  entity_type: string;
  source: string;
  classification: string;
  latitude: number;
  longitude: number;
  heading: number;
  speed_knots: number;
  mil_std_2525d_symbol: string;
  affiliations: string[];
}

const sampleEntities: SeedEntity[] = [
  // Vessels - Persian Gulf
  {
    name: 'MV ATLANTIC VOYAGER',
    entity_type: 'VESSEL',
    source: 'GEOINT',
    classification: 'UNCLASSIFIED',
    latitude: 26.234,
    longitude: 53.123,
    heading: 315,
    speed_knots: 12.5,
    mil_std_2525d_symbol: 'SFSPCLFF--',
    affiliations: ['COMMERCIAL', 'FLAGGED_PANAMA'],
  },
  {
    name: 'DHOW-7742',
    entity_type: 'VESSEL',
    source: 'SIGINT',
    classification: 'SECRET',
    latitude: 25.876,
    longitude: 54.321,
    heading: 180,
    speed_knots: 6.2,
    mil_std_2525d_symbol: 'SHSPCLFF--',
    affiliations: ['SUSPICIOUS', 'FLAGGED_UNKNOWN'],
  },
  {
    name: 'USS MASON (DDG-87)',
    entity_type: 'VESSEL',
    source: 'MANUAL',
    classification: 'UNCLASSIFIED',
    latitude: 25.551,
    longitude: 52.887,
    heading: 90,
    speed_knots: 18.0,
    mil_std_2525d_symbol: 'SFSPCLDD--',
    affiliations: ['USN', 'FIFTH_FLEET'],
  },
  // Aircraft - Middle East
  {
    name: 'EAGLE-01',
    entity_type: 'AIRCRAFT',
    source: 'SIGINT',
    classification: 'SECRET',
    latitude: 33.315,
    longitude: 44.366,
    heading: 270,
    speed_knots: 420,
    mil_std_2525d_symbol: 'SFAPMFF---',
    affiliations: ['COALITION', 'ISR'],
  },
  {
    name: 'UAE-117',
    entity_type: 'AIRCRAFT',
    source: 'OSINT',
    classification: 'UNCLASSIFIED',
    latitude: 24.433,
    longitude: 54.651,
    heading: 45,
    speed_knots: 350,
    mil_std_2525d_symbol: 'SFAPMFF---',
    affiliations: ['UAE_AF'],
  },
  // Vehicles - Syria
  {
    name: 'CONVOY-ALPHA',
    entity_type: 'VEHICLE',
    source: 'GEOINT',
    classification: 'SECRET',
    latitude: 35.123,
    longitude: 36.789,
    heading: 135,
    speed_knots: 25,
    mil_std_2525d_symbol: 'SHGPUCVR--',
    affiliations: ['MILITARY', 'HOSTILE'],
  },
  {
    name: 'SUPPLY-TRUCK-22',
    entity_type: 'VEHICLE',
    source: 'HUMINT',
    classification: 'CONFIDENTIAL',
    latitude: 35.234,
    longitude: 36.891,
    heading: 90,
    speed_knots: 35,
    mil_std_2525d_symbol: 'SFGPUCVR--',
    affiliations: ['LOGISTICS', 'FRIENDLY'],
  },
  // People
  {
    name: 'HVT-ALPHA',
    entity_type: 'PERSON',
    source: 'HUMINT',
    classification: 'TOP_SECRET',
    latitude: 33.513,
    longitude: 36.292,
    heading: 0,
    speed_knots: 0,
    mil_std_2525d_symbol: 'SHGPUCP---',
    affiliations: ['HIGH_VALUE_TARGET'],
  },
  {
    name: 'INFORMANT-BRAVO',
    entity_type: 'PERSON',
    source: 'HUMINT',
    classification: 'SECRET',
    latitude: 33.888,
    longitude: 35.495,
    heading: 0,
    speed_knots: 0,
    mil_std_2525d_symbol: 'SFGPUCP---',
    affiliations: ['ASSET', 'FRIENDLY'],
  },
  // Facilities
  {
    name: 'COMPOUND-DELTA',
    entity_type: 'FACILITY',
    source: 'GEOINT',
    classification: 'SECRET',
    latitude: 34.789,
    longitude: 38.123,
    heading: 0,
    speed_knots: 0,
    mil_std_2525d_symbol: 'SHGPI-----',
    affiliations: ['HOSTILE', 'WEAPONS_STORAGE'],
  },
  {
    name: 'FOB LIBERTY',
    entity_type: 'FACILITY',
    source: 'MANUAL',
    classification: 'CONFIDENTIAL',
    latitude: 33.298,
    longitude: 44.395,
    heading: 0,
    speed_knots: 0,
    mil_std_2525d_symbol: 'SFGPI-----',
    affiliations: ['FRIENDLY', 'COALITION_BASE'],
  },
  // Signals
  {
    name: 'EMITTER-7790',
    entity_type: 'SIGNAL',
    source: 'SIGINT',
    classification: 'TOP_SECRET',
    latitude: 35.678,
    longitude: 37.456,
    heading: 0,
    speed_knots: 0,
    mil_std_2525d_symbol: 'SHSPS-----',
    affiliations: ['RADAR', 'AIR_DEFENSE'],
  },
  // Units
  {
    name: '3RD BDE, 1ST AD',
    entity_type: 'UNIT',
    source: 'MANUAL',
    classification: 'CONFIDENTIAL',
    latitude: 32.456,
    longitude: 44.789,
    heading: 0,
    speed_knots: 0,
    mil_std_2525d_symbol: 'SFGPUCA---',
    affiliations: ['US_ARMY', '1ST_ARMORED_DIV'],
  },
];

async function seed() {
  const client = new Client({
    host: process.env.DATABASE_HOST || 'localhost',
    port: parseInt(process.env.DATABASE_PORT || '5432'),
    database: process.env.DATABASE_NAME || 'sentinel',
    user: process.env.DATABASE_USER || 'sentinel',
    password: process.env.DATABASE_PASSWORD || 'sentinel_dev',
  });

  try {
    await client.connect();
    console.log('Connected to database');

    // Ensure schema exists
    await client.query('CREATE SCHEMA IF NOT EXISTS sentinel');

    // Create entities table if not exists
    await client.query(`
      CREATE TABLE IF NOT EXISTS sentinel.entities (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        entity_type VARCHAR(50) NOT NULL,
        name VARCHAR(255) NOT NULL,
        description TEXT,
        source VARCHAR(50) NOT NULL,
        classification VARCHAR(50) NOT NULL DEFAULT 'UNCLASSIFIED',
        position geometry(Point, 4326),
        heading FLOAT,
        speed_knots FLOAT,
        course FLOAT,
        mil_std_2525d_symbol VARCHAR(50),
        metadata JSONB DEFAULT '{}',
        affiliations TEXT[] DEFAULT '{}',
        created_at TIMESTAMPTZ DEFAULT NOW(),
        updated_at TIMESTAMPTZ DEFAULT NOW(),
        last_seen_at TIMESTAMPTZ DEFAULT NOW()
      )
    `);

    // Create spatial index
    await client.query(`
      CREATE INDEX IF NOT EXISTS idx_entities_position
      ON sentinel.entities USING GIST (position)
    `);

    // Insert sample entities
    for (const entity of sampleEntities) {
      await client.query(
        `INSERT INTO sentinel.entities
          (entity_type, name, source, classification, position, heading, speed_knots, mil_std_2525d_symbol, affiliations, last_seen_at)
        VALUES
          ($1, $2, $3, $4, ST_SetSRID(ST_MakePoint($5, $6), 4326), $7, $8, $9, $10, NOW())
        ON CONFLICT DO NOTHING`,
        [
          entity.entity_type,
          entity.name,
          entity.source,
          entity.classification,
          entity.longitude,
          entity.latitude,
          entity.heading,
          entity.speed_knots,
          entity.mil_std_2525d_symbol,
          entity.affiliations,
        ],
      );
      console.log(`  Inserted: ${entity.name} (${entity.entity_type})`);
    }

    // Create track_points hypertable
    await client.query(`
      CREATE TABLE IF NOT EXISTS sentinel.track_points (
        id UUID DEFAULT gen_random_uuid(),
        entity_id UUID NOT NULL,
        position geometry(Point, 4326) NOT NULL,
        heading FLOAT,
        speed_knots FLOAT,
        course FLOAT,
        source VARCHAR(50),
        timestamp TIMESTAMPTZ NOT NULL
      )
    `);

    // Convert to hypertable (if not already)
    await client.query(`
      SELECT create_hypertable('sentinel.track_points', 'timestamp',
        if_not_exists => TRUE,
        migrate_data => TRUE
      )
    `).catch(() => console.log('  Hypertable already exists or TimescaleDB not available'));

    // Create alerts table
    await client.query(`
      CREATE TABLE IF NOT EXISTS sentinel.alerts (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        alert_type VARCHAR(50) NOT NULL,
        severity VARCHAR(20) NOT NULL,
        title VARCHAR(500) NOT NULL,
        description TEXT,
        entity_id UUID,
        related_entity_ids UUID[] DEFAULT '{}',
        position geometry(Point, 4326),
        rule_id UUID,
        metadata JSONB DEFAULT '{}',
        created_at TIMESTAMPTZ DEFAULT NOW(),
        acknowledged_at TIMESTAMPTZ,
        acknowledged_by VARCHAR(255),
        resolved_at TIMESTAMPTZ
      )
    `);

    // Create alert_rules table
    await client.query(`
      CREATE TABLE IF NOT EXISTS sentinel.alert_rules (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        name VARCHAR(255) NOT NULL,
        rule_type VARCHAR(50) NOT NULL,
        config JSONB NOT NULL DEFAULT '{}',
        monitored_entity_types TEXT[] DEFAULT '{}',
        severity VARCHAR(20) NOT NULL DEFAULT 'MEDIUM',
        enabled BOOLEAN DEFAULT TRUE,
        created_at TIMESTAMPTZ DEFAULT NOW(),
        updated_at TIMESTAMPTZ DEFAULT NOW()
      )
    `);

    // Create links table
    await client.query(`
      CREATE TABLE IF NOT EXISTS sentinel.links (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        source_entity_id UUID NOT NULL,
        target_entity_id UUID NOT NULL,
        link_type VARCHAR(50) NOT NULL,
        confidence FLOAT DEFAULT 0.5,
        description TEXT,
        evidence TEXT[] DEFAULT '{}',
        first_observed TIMESTAMPTZ,
        last_observed TIMESTAMPTZ,
        metadata JSONB DEFAULT '{}',
        created_at TIMESTAMPTZ DEFAULT NOW()
      )
    `);

    // Insert sample geofence rule
    await client.query(`
      INSERT INTO sentinel.alert_rules (name, rule_type, config, monitored_entity_types, severity)
      VALUES (
        'Persian Gulf Exclusion Zone',
        'GEOFENCE_BREACH',
        '{"polygon": [{"latitude": 27.0, "longitude": 51.0}, {"latitude": 27.0, "longitude": 56.0}, {"latitude": 24.0, "longitude": 56.0}, {"latitude": 24.0, "longitude": 51.0}], "trigger_on_enter": true, "trigger_on_exit": false}',
        ARRAY['VESSEL', 'AIRCRAFT'],
        'HIGH'
      )
      ON CONFLICT DO NOTHING
    `);
    console.log('  Inserted sample geofence rule');

    // Create locations table
    await client.query(`
      CREATE TABLE IF NOT EXISTS sentinel.locations (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        name VARCHAR(255) NOT NULL,
        description TEXT,
        latitude FLOAT NOT NULL,
        longitude FLOAT NOT NULL,
        altitude FLOAT DEFAULT 1000,
        heading FLOAT DEFAULT 0,
        pitch FLOAT DEFAULT -45,
        "range" FLOAT DEFAULT 2000,
        "has3dTiles" BOOLEAN DEFAULT FALSE,
        category VARCHAR(50) DEFAULT 'CUSTOM',
        "createdBy" VARCHAR(255),
        "createdAt" TIMESTAMPTZ DEFAULT NOW(),
        "updatedAt" TIMESTAMPTZ DEFAULT NOW()
      )
    `);

    // Seed locations
    const seedLocations = [
      { name: 'New York City', description: 'Financial capital, largest US city', latitude: 40.7128, longitude: -74.0060, altitude: 1500, heading: 0, pitch: -45, range: 3000, has3dTiles: true, category: 'CITY' },
      { name: 'London', description: 'Capital of the United Kingdom', latitude: 51.5074, longitude: -0.1278, altitude: 1500, heading: 0, pitch: -45, range: 3000, has3dTiles: true, category: 'CITY' },
      { name: 'Dubai', description: 'Major city in the United Arab Emirates', latitude: 25.2048, longitude: 55.2708, altitude: 1500, heading: 0, pitch: -45, range: 3000, has3dTiles: true, category: 'CITY' },
      { name: 'Washington DC', description: 'Capital of the United States', latitude: 38.9072, longitude: -77.0369, altitude: 1200, heading: 0, pitch: -45, range: 2500, has3dTiles: true, category: 'CITY' },
      { name: 'Tokyo', description: 'Capital of Japan', latitude: 35.6762, longitude: 139.6503, altitude: 1500, heading: 0, pitch: -45, range: 3000, has3dTiles: true, category: 'CITY' },
      { name: 'Sydney', description: 'Largest city in Australia', latitude: -33.8688, longitude: 151.2093, altitude: 1500, heading: 0, pitch: -45, range: 3000, has3dTiles: true, category: 'CITY' },
      { name: 'Singapore', description: 'City-state in Southeast Asia', latitude: 1.3521, longitude: 103.8198, altitude: 1200, heading: 0, pitch: -45, range: 2500, has3dTiles: true, category: 'CITY' },
      { name: 'Paris', description: 'Capital of France', latitude: 48.8566, longitude: 2.3522, altitude: 1200, heading: 0, pitch: -45, range: 2500, has3dTiles: true, category: 'CITY' },
      { name: 'Naval Station Norfolk', description: 'Largest naval base in the world, home of US Atlantic Fleet', latitude: 36.9461, longitude: -76.3013, altitude: 800, heading: 0, pitch: -35, range: 2000, has3dTiles: false, category: 'MILITARY_BASE' },
      { name: 'Pearl Harbor', description: 'Joint Base Pearl Harbor-Hickam, headquarters of US Pacific Fleet', latitude: 21.3500, longitude: -157.9500, altitude: 800, heading: 0, pitch: -35, range: 2000, has3dTiles: false, category: 'MILITARY_BASE' },
    ];

    for (const loc of seedLocations) {
      await client.query(
        `INSERT INTO sentinel.locations
          (name, description, latitude, longitude, altitude, heading, pitch, "range", "has3dTiles", category)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
        ON CONFLICT DO NOTHING`,
        [loc.name, loc.description, loc.latitude, loc.longitude, loc.altitude, loc.heading, loc.pitch, loc.range, loc.has3dTiles, loc.category],
      );
      console.log(`  Inserted location: ${loc.name} (${loc.category})`);
    }

    console.log(`\nSeeded ${sampleEntities.length} entities and ${seedLocations.length} locations successfully`);
  } catch (err) {
    console.error('Seed failed:', err);
    process.exit(1);
  } finally {
    await client.end();
  }
}

seed();
