import { NAV_GROUPS, routeForShortcut } from './nav-rail.service';

describe('routeForShortcut', () => {
  const press = (key: string, over: Partial<{ ctrlKey: boolean; metaKey: boolean; altKey: boolean; target: EventTarget | null }> = {}) =>
    routeForShortcut({ key, ctrlKey: false, metaKey: false, altKey: false, target: document.body, ...over });

  it('maps every nav item key to its route, case-insensitively', () => {
    for (const group of NAV_GROUPS) {
      for (const item of group.items) {
        expect(press(item.key)).toBe(item.path);
        expect(press(item.key.toUpperCase())).toBe(item.path);
      }
    }
  });

  it('opens the geofences panel on G, and E opens nothing now that emplacements live in their connections', () => {
    expect(press('g')).toBe('/geofences');
    expect(press('e')).toBeNull();
  });

  it('ignores keys with a modifier held, so browser shortcuts still work', () => {
    expect(press('f', { ctrlKey: true })).toBeNull();
    expect(press('f', { metaKey: true })).toBeNull();
    expect(press('f', { altKey: true })).toBeNull();
  });

  it('ignores keys typed into a field', () => {
    expect(press('f', { target: document.createElement('input') })).toBeNull();
    expect(press('f', { target: document.createElement('textarea') })).toBeNull();
    expect(press('f', { target: document.createElement('select') })).toBeNull();
    const editable = document.createElement('div');
    editable.setAttribute('contenteditable', 'true');
    expect(press('f', { target: editable })).toBeNull();
  });

  it('ignores keys that are not shortcuts', () => {
    expect(press('x')).toBeNull();
    expect(press('Escape')).toBeNull();
  });
});
