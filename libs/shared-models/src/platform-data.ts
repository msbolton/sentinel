import { NavigationalStatus } from './common';

/** AIS maritime identification and voyage data (per UC2 AISDataType) */
export interface AISData {
  // Identity
  mmsi: string;
  imo?: string;
  callsign?: string;
  vesselName?: string;

  // Classification
  shipType?: number;
  shipTypeName?: string;
  flag?: string;

  // Voyage
  destination?: string;
  eta?: string;
  draught?: number;

  // Dimensions (UC2: dimensionA-D from reference point)
  dimensionA?: number;
  dimensionB?: number;
  dimensionC?: number;
  dimensionD?: number;
  lengthOverall?: number;
  beam?: number;

  // Navigation
  navStatus?: NavigationalStatus;
  rateOfTurn?: number;
  speedOverGround?: number;
  courseOverGround?: number;
  trueHeading?: number;
  positionAccuracyHigh?: boolean;
  specialManoeuvre?: boolean;

  // Message context
  messageType?: number;
  repeatIndicator?: number;
}

/** ADS-B / IFF transponder data (per UC2 IFFDataType + ModeSType + Mode5Type) */
export interface ADSBData {
  // Core identity
  icaoHex: string;
  registration?: string;
  aircraftType?: string;
  aircraftTypeName?: string;
  operatorIcao?: string;
  operatorName?: string;

  // Transponder codes (UC2 IFFDataType)
  squawk?: string;
  emergency?: string;
  mode1?: string;
  mode2?: string;

  // Mode S data (UC2 ModeSType)
  aircraftId?: string;
  flightAirborne?: boolean;
  indicatedAirSpeed?: number;
  trueAirSpeed?: number;
  groundSpeed?: number;
  magneticHeading?: number;

  // Mode 5 data (UC2 Mode5Type — military IFF)
  mode5FigureOfMerit?: number;
  nationalOriginCode?: number;
  missionCode?: number;

  // Position/altitude
  altitudeBaro?: number;
  altitudeGeom?: number;
  verticalRate?: number;
  onGround?: boolean;

  // Quality indicators
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

/** Satellite TLE orbital data */
export interface TLEData {
  // Identity
  noradId: number;
  intlDesignator?: string;
  satName?: string;

  // TLE elements
  line1: string;
  line2: string;
  epoch?: string;

  // Orbital elements (derived)
  inclination?: number;
  eccentricity?: number;
  raan?: number;
  argOfPerigee?: number;
  meanAnomaly?: number;
  meanMotion?: number;
  period?: number;
  apogee?: number;
  perigee?: number;

  // Classification
  objectType?: string;
  rcsSize?: string;
  launchDate?: string;
  decayDate?: string;
  country?: string;
}

/** Link 16 / JREAP-C tactical data link */
export interface Link16Data {
  trackNumber: number;
  jSeriesLabel: string;
  originatingUnit?: string;
  quality?: number;
  exerciseIndicator?: boolean;
  simulationIndicator?: boolean;
  forceIdentity?: string;
}

/** Cursor on Target metadata */
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

/** UAV-specific telemetry data (per UC2 UAVDataType) */
export interface UAVData {
  make?: string;
  model?: string;
  serialNumber?: string;
  macAddress?: string;
  operatingFrequency?: number;
  frequencyRange?: number;
}

/** Discriminated union — only one populated per entity */
export interface PlatformData {
  ais?: AISData;
  adsb?: ADSBData;
  tle?: TLEData;
  link16?: Link16Data;
  cot?: CoTData;
  uav?: UAVData;
}
