import { EntityFocusService, FocusTarget, canFocus } from './entity-focus.service';

describe('EntityFocusService', () => {
  it('passes on targets with an entity or a position, and nothing else', () => {
    const service = new EntityFocusService();
    const seen: FocusTarget[] = [];
    service.focus$.subscribe((t) => seen.push(t));

    expect(service.focus({ entityId: 'e1', label: 'UAL1' })).toBe(true);
    expect(service.focus({ position: { latitude: 50, longitude: 1 }, label: 'Breach' })).toBe(true);
    expect(service.focus({ label: 'Nothing' })).toBe(false);

    expect(seen.map((t) => t.label)).toEqual(['UAL1', 'Breach']);
    expect(canFocus({})).toBe(false);
  });
});
