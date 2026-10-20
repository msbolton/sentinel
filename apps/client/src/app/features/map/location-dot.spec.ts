import { locationDotDataUrl, locationDotSvg } from './location-dot';

describe('locationDot', () => {
  it('renders three concentric circles in the requested hex color', () => {
    const blue = locationDotSvg('#3b82f6');
    expect(blue).toContain('rgba(59,130,246,0.12)');
    expect(blue).toContain('rgba(59,130,246,0.2)');
    expect(blue).toContain('fill="#3b82f6"');

    const violet = locationDotSvg('#8b5cf6');
    expect(violet).toContain('rgba(139,92,246,0.12)');
    expect(violet).toContain('rgba(139,92,246,0.2)');
    expect(violet).toContain('fill="#8b5cf6"');
  });

  it('caches the data URL by color', () => {
    const a = locationDotDataUrl('#8b5cf6');
    const b = locationDotDataUrl('#8b5cf6');
    const c = locationDotDataUrl('#3b82f6');
    expect(a).toBe(b);
    expect(a).not.toBe(c);
    expect(a.startsWith('data:image/svg+xml;base64,')).toBe(true);
  });
});
