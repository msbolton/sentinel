import { TestBed } from '@angular/core/testing';
import { NAV_GROUPS, NavRailService, routeForShortcut } from './nav-rail.service';

describe('NavRailService', () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.style.removeProperty('--sidebar-width');
    TestBed.resetTestingModule();
  });

  it('starts expanded and publishes the rail width as --sidebar-width', () => {
    const rail = TestBed.inject(NavRailService);
    expect(rail.expanded()).toBe(true);
    expect(document.documentElement.style.getPropertyValue('--sidebar-width')).toBe('208px');
  });

  it('collapses on toggle and narrows the rail', () => {
    const rail = TestBed.inject(NavRailService);
    rail.toggle();
    expect(rail.expanded()).toBe(false);
    expect(document.documentElement.style.getPropertyValue('--sidebar-width')).toBe('56px');
  });

  it('remembers a collapsed rail across reloads', () => {
    TestBed.inject(NavRailService).toggle();
    TestBed.resetTestingModule();
    expect(TestBed.inject(NavRailService).expanded()).toBe(false);
  });
});

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
