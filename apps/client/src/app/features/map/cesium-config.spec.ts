import { COPERNICUS_CREDIT } from './cesium-config';

describe('COPERNICUS_CREDIT', () => {
  it('is the licence attribution, word for word', () => {
    expect(COPERNICUS_CREDIT).toBe(
      'Produced using Copernicus WorldDEM-30 © DLR e.V. 2010-2014 and © Airbus Defence and Space GmbH 2014-2018 provided under COPERNICUS by the European Union and ESA; all rights reserved.',
    );
  });
});
