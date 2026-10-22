import { DROP_LINE_CONFIG, dropLinePoints, showsDropLine } from './drop-line';
import { formatAltitude } from '../../shared/geo-format';
import { Classification, Entity, EntityType } from '../../shared/models/entity.model';

describe('dropLinePoints', () => {
  it('runs from the satellite straight down to its ground point, labelled at the midpoint', () => {
    expect(dropLinePoints({ lat: 51.6, lon: -0.1, height: 408_000 })).toEqual({
      top: { lat: 51.6, lon: -0.1, height: 408_000 },
      ground: { lat: 51.6, lon: -0.1, height: 0 },
      mid: { lat: 51.6, lon: -0.1, height: 204_000 },
      label: '408 km',
    });
  });
});

describe('dropLinePoints over terrain', () => {
  it('ends on the provided ground height, with the label at the midpoint of the line', () => {
    expect(dropLinePoints({ lat: 46.5, lon: 8.0, height: 1_400 }, 600)).toEqual({
      top: { lat: 46.5, lon: 8.0, height: 1_400 },
      ground: { lat: 46.5, lon: 8.0, height: 600 },
      mid: { lat: 46.5, lon: 8.0, height: 1_000 },
      label: formatAltitude(1_400),
    });
  });

  it('treats a missing or non-finite ground height as sea level', () => {
    expect(dropLinePoints({ lat: 1, lon: 2, height: 100 }).ground.height).toBe(0);
    expect(dropLinePoints({ lat: 1, lon: 2, height: 100 }, Number.NaN).ground.height).toBe(0);
  });
});

describe('showsDropLine', () => {
  const entity = (over: Partial<Entity>): Entity =>
    ({ id: 'e', name: 'E', entityType: EntityType.AIRCRAFT, source: 'MANUAL' as Entity['source'], classification: Classification.UNCLASSIFIED, metadata: {}, affiliations: [], createdAt: '', updatedAt: '', ...over }) as Entity;

  it('is shown for aircraft and drones that are off the ground', () => {
    expect(showsDropLine(entity({ position: { latitude: 1, longitude: 2, altitude: 9000 } }))).toBe(true);
    expect(showsDropLine(entity({ entityType: EntityType.DRONE, position: { latitude: 1, longitude: 2, altitude: 120 } }))).toBe(true);
  });

  it('is shown for every satellite, which has no stored position until it is propagated', () => {
    expect(showsDropLine(entity({ entityType: EntityType.SATELLITE }))).toBe(true);
  });

  it('is not shown for an aircraft on the ground or without an altitude', () => {
    expect(showsDropLine(entity({ position: { latitude: 1, longitude: 2, altitude: 0 } }))).toBe(false);
    expect(showsDropLine(entity({ position: { latitude: 1, longitude: 2 } }))).toBe(false);
  });

  it('is never shown for surface entities', () => {
    expect(showsDropLine(entity({ entityType: EntityType.VESSEL, position: { latitude: 1, longitude: 2, altitude: 30 } }))).toBe(false);
    expect(showsDropLine(entity({ entityType: EntityType.FACILITY, position: { latitude: 1, longitude: 2, altitude: 400 } }))).toBe(false);
  });
});

describe('DROP_LINE_CONFIG', () => {
  it('draws the line in the yellow accent', () => {
    expect(DROP_LINE_CONFIG.color).toBe('#f59e0b');
  });
});
