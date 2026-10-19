/** Whether an emplacement stays where it was surveyed. */
export type Mobility = 'FIXED' | 'MOBILE';
export type SensorType = 'RADAR' | 'EO_IR' | 'RF' | 'ACOUSTIC' | 'AIS_RECEIVER' | 'ADSB_RECEIVER' | 'HUMAN' | 'OTHER';
export type AzimuthReference = 'TRUE_NORTH' | 'PLATFORM';

export const SENSOR_TYPES: SensorType[] = ['RADAR', 'EO_IR', 'RF', 'ACOUSTIC', 'AIS_RECEIVER', 'ADSB_RECEIVER', 'HUMAN', 'OTHER'];

export const SENSOR_TYPE_LABELS: Record<SensorType, string> = {
  RADAR: 'Radar', EO_IR: 'EO/IR', RF: 'RF', ACOUSTIC: 'Acoustic',
  AIS_RECEIVER: 'AIS receiver', ADSB_RECEIVER: 'ADS-B receiver', HUMAN: 'Observer', OTHER: 'Other',
};

export interface EmplacementPoint { lat: number; lon: number }

/** Around the boresight; azimuthWidthDeg 360 is omni; all zeros is no coverage. */
export interface EmplacementCoverage {
  minRangeM: number;
  maxRangeM: number;
  azimuthWidthDeg: number;
  elevationMinDeg?: number;
  elevationMaxDeg?: number;
}

export interface EmplacementSigma { rangeM?: number; azimuthDeg?: number; elevationDeg?: number }

/** One emplacement as /api/v1/emplacements returns it. */
export interface Emplacement {
  id: string;
  name: string;
  description: string;
  mobility: Mobility;
  sensorType: SensorType;
  position: EmplacementPoint | null;
  altitude: number | null;
  boresightDeg: number;
  azimuthReference: AzimuthReference;
  coverage: EmplacementCoverage;
  defaultSigma: EmplacementSigma;
  feedId: string | null;
  classification: string;
  exercise: boolean;
  active: boolean;
  lastReportAt: string | null;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  version: number;
}

/** A create or PATCH body: what is left out is left as it is; feedId '' unbinds. */
export interface EmplacementInput {
  name?: string;
  description?: string;
  mobility?: Mobility;
  sensorType?: SensorType;
  position?: EmplacementPoint;
  altitude?: number;
  boresightDeg?: number;
  azimuthReference?: AzimuthReference;
  coverage?: EmplacementCoverage;
  defaultSigma?: EmplacementSigma;
  feedId?: string;
  classification?: string;
  exercise?: boolean;
  active?: boolean;
}

/** The form's state; id is null for a new emplacement. */
export interface EmplacementDraft {
  id: string | null;
  name: string;
  description: string;
  mobility: Mobility;
  sensorType: SensorType;
  position: EmplacementPoint | null;
  altitude: number | null;
  boresightDeg: number;
  azimuthReference: AzimuthReference;
  coverage: EmplacementCoverage;
  defaultSigma: EmplacementSigma;
  feedId: string | null;
  classification: string;
  exercise: boolean;
  active: boolean;
}

export function newDraft(): EmplacementDraft {
  return {
    id: null, name: '', description: '', mobility: 'FIXED', sensorType: 'RADAR', position: null, altitude: null,
    boresightDeg: 0, azimuthReference: 'TRUE_NORTH', coverage: { minRangeM: 0, maxRangeM: 50_000, azimuthWidthDeg: 120 },
    defaultSigma: {}, feedId: null, classification: 'UNCLASSIFIED', exercise: false, active: true,
  };
}

export function draftOf(e: Emplacement): EmplacementDraft {
  return {
    id: e.id, name: e.name, description: e.description, mobility: e.mobility, sensorType: e.sensorType,
    position: e.position ? { ...e.position } : null, altitude: e.altitude, boresightDeg: e.boresightDeg,
    azimuthReference: e.azimuthReference, coverage: { ...e.coverage }, defaultSigma: { ...e.defaultSigma },
    feedId: e.feedId, classification: e.classification, exercise: e.exercise, active: e.active,
  };
}

/** The body the form saves. A coverage with no reach is sent as no coverage. */
export function inputOf(d: EmplacementDraft): EmplacementInput {
  const coverage = d.coverage.maxRangeM > 0 ? d.coverage : { minRangeM: 0, maxRangeM: 0, azimuthWidthDeg: 0 };
  const input: EmplacementInput = {
    name: d.name, description: d.description, mobility: d.mobility, sensorType: d.sensorType,
    boresightDeg: d.boresightDeg, azimuthReference: d.azimuthReference, coverage, defaultSigma: d.defaultSigma,
    feedId: d.feedId ?? '', classification: d.classification, exercise: d.exercise, active: d.active,
  };
  if (d.position) input.position = d.position;
  if (d.altitude != null) input.altitude = d.altitude;
  return input;
}

/** The emplacement an entity stands for (its EMPLACEMENT identity), or null. */
export function emplacementIdOf(entity: { identities?: { type: string; value: string }[] | null }): string | null {
  return entity.identities?.find((i) => i?.type === 'EMPLACEMENT')?.value ?? null;
}

export function coverageSummary(c: EmplacementCoverage): string {
  if (!(c.maxRangeM > 0)) return 'no coverage';
  const reach = c.maxRangeM >= 1000 ? `${Math.round(c.maxRangeM / 100) / 10} km` : `${Math.round(c.maxRangeM)} m`;
  return `${c.azimuthWidthDeg >= 360 ? 'omni' : `${Math.round(c.azimuthWidthDeg)}°`} · ${reach}`;
}
