import { dropLinePoints } from './satellite-drop-line';

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
