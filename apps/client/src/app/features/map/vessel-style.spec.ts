import { Entity, EntityType } from '../../shared/models/entity.model';
import { VESSEL_CATEGORY_COLORS } from '../../shared/vessel-format';
import { vesselColor, vesselHeading } from './vessel-style';

function vessel(over: Partial<Entity> & { ais?: Record<string, unknown> } = {}): Entity {
  const { ais, ...rest } = over;
  return {
    id: 'v1', name: 'V', entityType: EntityType.VESSEL, source: 'AIS',
    platformData: { ais: { mmsi: '244123456', ...(ais ?? {}) } },
    ...rest,
  } as unknown as Entity;
}

describe('vessel style', () => {
  it('colours vessels by ship type category', () => {
    expect(vesselColor(vessel({ ais: { shipType: 71 } }))).toBe(VESSEL_CATEGORY_COLORS.cargo);
    expect(vesselColor(vessel({ ais: { shipType: 80 } }))).toBe(VESSEL_CATEGORY_COLORS.tanker);
    expect(vesselColor(vessel())).toBe(VESSEL_CATEGORY_COLORS.other);
  });

  it('points moving vessels along their heading and leaves stopped ones alone', () => {
    expect(vesselHeading(vessel({ heading: 270, speedKnots: 12 }))).toBe(270);
    expect(vesselHeading(vessel({ heading: 120, speedKnots: 0.1 }))).toBeNull();
    expect(vesselHeading(vessel({ heading: 120, speedKnots: 0.1, ais: { trueHeading: 120 } }))).toBe(120);
    expect(vesselHeading(vessel({ heading: 120, speedKnots: 0, ais: { trueHeading: 511 } }))).toBeNull();
    expect(vesselHeading(vessel({ speedKnots: 12 }))).toBeNull();
    expect(vesselHeading({ ...vessel({ heading: 90, speedKnots: 10 }), entityType: EntityType.AIRCRAFT } as Entity)).toBeNull();
  });
});
