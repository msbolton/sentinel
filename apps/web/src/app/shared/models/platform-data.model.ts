// --- Enums ---

export enum TrackEnvironment {
  AIR = 'AIR',
  SEA_SURFACE = 'SEA_SURFACE',
  SUBSURFACE = 'SUBSURFACE',
  GROUND = 'GROUND',
  SPACE = 'SPACE',
  UNKNOWN = 'UNKNOWN',
}

export enum OperationalStatus {
  OPERATIONAL = 'OPERATIONAL',
  DEGRADED = 'DEGRADED',
  DAMAGED = 'DAMAGED',
  DESTROYED = 'DESTROYED',
  INACTIVE = 'INACTIVE',
  UNKNOWN = 'UNKNOWN',
}

export enum DamageAssessment {
  NONE = 'NONE',
  LIGHT = 'LIGHT',
  MODERATE = 'MODERATE',
  HEAVY = 'HEAVY',
  DESTROYED = 'DESTROYED',
  UNKNOWN = 'UNKNOWN',
}

export enum Affiliation {
  FRIENDLY = 'FRIENDLY',
  HOSTILE = 'HOSTILE',
  NEUTRAL = 'NEUTRAL',
  UNKNOWN = 'UNKNOWN',
  ASSUMED_FRIENDLY = 'ASSUMED_FRIENDLY',
  SUSPECT = 'SUSPECT',
  PENDING = 'PENDING',
}

export enum NavigationalStatus {
  UNDER_WAY_USING_ENGINE = 'UNDER_WAY_USING_ENGINE',
  AT_ANCHOR = 'AT_ANCHOR',
  NOT_UNDER_COMMAND = 'NOT_UNDER_COMMAND',
  RESTRICTED_MANOEUVRABILITY = 'RESTRICTED_MANOEUVRABILITY',
  CONSTRAINED_BY_DRAUGHT = 'CONSTRAINED_BY_DRAUGHT',
  MOORED = 'MOORED',
  AGROUND = 'AGROUND',
  ENGAGED_IN_FISHING = 'ENGAGED_IN_FISHING',
  UNDER_WAY_SAILING = 'UNDER_WAY_SAILING',
  AIS_SART = 'AIS_SART',
  UNKNOWN = 'UNKNOWN',
}

// --- Platform Data Interfaces ---

export interface AISData {
  mmsi: string;
  imo?: string;
  callsign?: string;
  vesselName?: string;
  shipType?: number;
  shipTypeName?: string;
  flag?: string;
  destination?: string;
  eta?: string;
  draught?: number;
  dimensionA?: number;
  dimensionB?: number;
  dimensionC?: number;
  dimensionD?: number;
  lengthOverall?: number;
  beam?: number;
  navStatus?: NavigationalStatus;
  rateOfTurn?: number;
  speedOverGround?: number;
  courseOverGround?: number;
  trueHeading?: number;
  positionAccuracyHigh?: boolean;
  specialManoeuvre?: boolean;
  messageType?: number;
  repeatIndicator?: number;
}

export interface ADSBData {
  icaoHex: string;
  registration?: string;
  aircraftType?: string;
  aircraftTypeName?: string;
  operatorIcao?: string;
  operatorName?: string;
  squawk?: string;
  emergency?: string;
  mode1?: string;
  mode2?: string;
  aircraftId?: string;
  flightAirborne?: boolean;
  indicatedAirSpeed?: number;
  trueAirSpeed?: number;
  groundSpeed?: number;
  magneticHeading?: number;
  mode5FigureOfMerit?: number;
  nationalOriginCode?: number;
  missionCode?: number;
  altitudeBaro?: number;
  altitudeGeom?: number;
  verticalRate?: number;
  onGround?: boolean;
  category?: string;
  nacP?: number;
  nacV?: number;
  sil?: number;
  silType?: string;
  nic?: number;
  rc?: number;
  gva?: number;
  sda?: number;
}

export interface TLEData {
  noradId: number;
  intlDesignator?: string;
  satName?: string;
  line1: string;
  line2: string;
  epoch?: string;
  inclination?: number;
  eccentricity?: number;
  raan?: number;
  argOfPerigee?: number;
  meanAnomaly?: number;
  meanMotion?: number;
  period?: number;
  apogee?: number;
  perigee?: number;
  objectType?: string;
  rcsSize?: string;
  launchDate?: string;
  decayDate?: string;
  country?: string;
}

export interface Link16Data {
  trackNumber: number;
  jSeriesLabel: string;
  originatingUnit?: string;
  quality?: number;
  exerciseIndicator?: boolean;
  simulationIndicator?: boolean;
  forceIdentity?: string;
}

export interface CoTData {
  uid: string;
  cotType: string;
  how?: string;
  ce?: number;
  le?: number;
  staleTime?: string;
  accessControl?: string;
  opex?: string;
  qos?: string;
}

export interface UAVData {
  make?: string;
  model?: string;
  serialNumber?: string;
  macAddress?: string;
  operatingFrequency?: number;
  frequencyRange?: number;
}

export interface PlatformData {
  ais?: AISData;
  adsb?: ADSBData;
  tle?: TLEData;
  link16?: Link16Data;
  cot?: CoTData;
  uav?: UAVData;
}

// --- Kinematics ---

export interface GeodeticVelocity {
  north: number;
  east: number;
  up: number;
}

export interface Orientation {
  yaw: number;
  pitch: number;
  roll: number;
}

export interface KinematicState {
  velocity?: GeodeticVelocity;
  acceleration?: { north: number; east: number; up: number };
}
