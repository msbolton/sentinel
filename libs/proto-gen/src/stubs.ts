/**
 * Stub enums for @sentinel/proto-gen.
 * These are used by Jest for testing when the real generated code
 * (libs/proto-gen/src/ts/) is not present (it's generated via `make proto`).
 * The jest.config.ts moduleNameMapper points @sentinel/proto-gen here.
 */

export enum EntityType {
  PERSON = 'PERSON',
  VEHICLE = 'VEHICLE',
  VESSEL = 'VESSEL',
  AIRCRAFT = 'AIRCRAFT',
  DRONE = 'DRONE',
  FACILITY = 'FACILITY',
  EQUIPMENT = 'EQUIPMENT',
  UNIT = 'UNIT',
  SIGNAL = 'SIGNAL',
  CYBER = 'CYBER',
  SENSOR = 'SENSOR',
  SATELLITE = 'SATELLITE',
  UNKNOWN = 'UNKNOWN',
}

export enum EntitySource {
  MANUAL = 'MANUAL',
  AIS = 'AIS',
  ADS_B = 'ADS_B',
  RADAR = 'RADAR',
  SIGINT = 'SIGINT',
  HUMINT = 'HUMINT',
  OSINT = 'OSINT',
  SENSOR = 'SENSOR',
  FEDERATION = 'FEDERATION',
  OPENSKY = 'OPENSKY',
  ADSB_LOL = 'ADSB_LOL',
  CELESTRAK = 'CELESTRAK',
  LINK16 = 'LINK16',
  GPS = 'GPS',
  UNKNOWN = 'UNKNOWN',
}

export enum Affiliation {
  PENDING = 'PENDING',
  UNKNOWN = 'UNKNOWN',
  ASSUMED_FRIEND = 'ASSUMED_FRIEND',
  FRIEND = 'FRIEND',
  NEUTRAL = 'NEUTRAL',
  SUSPECT = 'SUSPECT',
  HOSTILE = 'HOSTILE',
  JOKER = 'JOKER',
  FAKER = 'FAKER',
}

export enum Classification {
  UNCLASSIFIED = 'UNCLASSIFIED',
  CONFIDENTIAL = 'CONFIDENTIAL',
  SECRET = 'SECRET',
  TOP_SECRET = 'TOP_SECRET',
}

export enum AlertSeverity {
  INFO = 'INFO',
  LOW = 'LOW',
  MEDIUM = 'MEDIUM',
  HIGH = 'HIGH',
  CRITICAL = 'CRITICAL',
}

export enum AlertType {
  GEOFENCE = 'GEOFENCE',
  PROXIMITY = 'PROXIMITY',
  SPEED = 'SPEED',
  PATTERN = 'PATTERN',
  MANUAL = 'MANUAL',
  SYSTEM = 'SYSTEM',
}

export enum LinkType {
  ASSOCIATION = 'ASSOCIATION',
  COMMUNICATION = 'COMMUNICATION',
  FINANCIAL = 'FINANCIAL',
  ORGANIZATIONAL = 'ORGANIZATIONAL',
  GEOGRAPHIC = 'GEOGRAPHIC',
  FAMILIAL = 'FAMILIAL',
  LOGISTIC = 'LOGISTIC',
  OPERATIONAL = 'OPERATIONAL',
  IDENTITY = 'IDENTITY',
}

export enum TrackQuality {
  LOW = 'LOW',
  MEDIUM = 'MEDIUM',
  HIGH = 'HIGH',
}

export enum Severity {
  LOW = 'LOW',
  MEDIUM = 'MEDIUM',
  HIGH = 'HIGH',
  CRITICAL = 'CRITICAL',
}
