import { COPERNICUS_CREDIT, ENTITY_TYPE_BILLBOARD_SVGS, HEADING_ROTATED_TYPES } from './cesium-config';
import { EntityType } from '../../shared/models/entity.model';

describe('COPERNICUS_CREDIT', () => {
  it('is the licence attribution, word for word', () => {
    expect(COPERNICUS_CREDIT).toBe(
      'Produced using Copernicus WorldDEM-30 © DLR e.V. 2010-2014 and © Airbus Defence and Space GmbH 2014-2018 provided under COPERNICUS by the European Union and ESA; all rights reserved.',
    );
  });
});

describe('vehicle billboard', () => {
  it('turns with the vehicle heading, like aircraft, drones and vessels', () => {
    expect(HEADING_ROTATED_TYPES.has(EntityType.VEHICLE)).toBe(true);
  });

  it('is a white top-down car with a black outline, so Cesium can tint it', () => {
    const svg = ENTITY_TYPE_BILLBOARD_SVGS[EntityType.VEHICLE];
    expect(svg).toContain('fill="white"');
    expect(svg).toContain('stroke="black"');
    expect(svg).toContain('<rect x="15" y="4" width="18" height="40" rx="7"/>');
  });
});
