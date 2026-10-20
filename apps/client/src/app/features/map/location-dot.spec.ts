import { locationDotDataUrl, locationDotSvg } from './location-dot';

describe('locationDot', () => {
  it('renders three concentric circles in the requested hex color, with optional dashed outer ring', () => {
    const blue = locationDotSvg('#3b82f6');
    expect(blue).toContain('rgba(59,130,246,0.12)');
    expect(blue).toContain('rgba(59,130,246,0.2)');
    expect(blue).toContain('fill="#3b82f6"');
    expect(blue).not.toContain('stroke-dasharray');

    const violet = locationDotSvg('#8b5cf6', { dashed: true });
    expect(violet).toContain('rgba(139,92,246,0.12)');
    expect(violet).toContain('rgba(139,92,246,0.2)');
    expect(violet).toContain('fill="#8b5cf6"');
    expect(violet).toContain('stroke-dasharray="4 3"');
  });

  it('caches the data URL by color and dashed option', () => {
    const a = locationDotDataUrl('#8b5cf6');
    const b = locationDotDataUrl('#8b5cf6');
    const c = locationDotDataUrl('#8b5cf6', { dashed: true });
    const d = locationDotDataUrl('#3b82f6');
    expect(a).toBe(b);
    expect(a).not.toBe(c);
    expect(a).not.toBe(d);
    expect(a.startsWith('data:image/svg+xml;base64,')).toBe(true);
  });
});
