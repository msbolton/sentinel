export enum EntityType {
  UNKNOWN = 'UNKNOWN',
  PERSON = 'PERSON',
  VEHICLE = 'VEHICLE',
  VESSEL = 'VESSEL',
  AIRCRAFT = 'AIRCRAFT',
  FACILITY = 'FACILITY',
  EQUIPMENT = 'EQUIPMENT',
  UNIT = 'UNIT',
  SIGNAL = 'SIGNAL',
  CYBER = 'CYBER',
}

export enum EntitySource {
  HUMINT = 'HUMINT',
  SIGINT = 'SIGINT',
  GEOINT = 'GEOINT',
  OSINT = 'OSINT',
  MASINT = 'MASINT',
  CYBER = 'CYBER',
  MANUAL = 'MANUAL',
  AIS = 'AIS',
  ADS_B = 'ADS_B',
  LINK16 = 'LINK16',
  GPS = 'GPS',
  RADAR = 'RADAR',
}

export enum Classification {
  UNCLASSIFIED = 'UNCLASSIFIED',
  CONFIDENTIAL = 'CONFIDENTIAL',
  SECRET = 'SECRET',
  TOP_SECRET = 'TOP_SECRET',
}

/**
 * Ordered classification levels for comparison queries.
 * Lower index = lower classification.
 */
export const CLASSIFICATION_ORDER: readonly Classification[] = [
  Classification.UNCLASSIFIED,
  Classification.CONFIDENTIAL,
  Classification.SECRET,
  Classification.TOP_SECRET,
] as const;

/**
 * Returns the numeric rank of a classification level (0-based).
 */
export function classificationRank(classification: Classification): number {
  const index = CLASSIFICATION_ORDER.indexOf(classification);
  return index === -1 ? 0 : index;
}
