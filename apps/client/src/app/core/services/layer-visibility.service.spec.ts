import { TestBed } from '@angular/core/testing';
import { LayerVisibilityService } from './layer-visibility.service';

const STORAGE_KEY = 'sentinel-layer-visibility';
const LEGACY_KEY = 'sentinel-feed-preferences';

describe('LayerVisibilityService', () => {
  beforeEach(() => {
    localStorage.clear();
    TestBed.configureTestingModule({});
  });

  afterEach(() => {
    localStorage.clear();
    jest.restoreAllMocks();
    TestBed.resetTestingModule();
  });

  function createService(): LayerVisibilityService {
    return TestBed.inject(LayerVisibilityService);
  }

  it('starts with nothing hidden', () => {
    const service = createService();
    expect(service.hidden().size).toBe(0);
    expect(service.isHidden('opensky')).toBe(false);
    expect(service.isVisible('opensky')).toBe(true);
  });

  it('persists hidden keys and restores them on the next construction', () => {
    const first = createService();
    first.hide('opensky');
    first.hide('ais-demo');
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!)).toEqual(['opensky', 'ais-demo']);

    TestBed.resetTestingModule();
    TestBed.configureTestingModule({});
    const second = createService();
    expect(second.isHidden('opensky')).toBe(true);
    expect(second.isHidden('ais-demo')).toBe(true);
    expect(second.isHidden('celestrak')).toBe(false);
  });

  it('removes the storage entry once nothing is hidden', () => {
    const service = createService();
    service.hide('opensky');
    service.show('opensky');
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it('deletes the legacy feed-preferences key on construction', () => {
    localStorage.setItem(LEGACY_KEY, JSON.stringify({ opensky: false }));
    createService();
    expect(localStorage.getItem(LEGACY_KEY)).toBeNull();
  });

  it('ignores malformed stored values', () => {
    localStorage.setItem(STORAGE_KEY, '{not json');
    expect(createService().hidden().size).toBe(0);

    TestBed.resetTestingModule();
    TestBed.configureTestingModule({});
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ opensky: true }));
    expect(createService().hidden().size).toBe(0);

    TestBed.resetTestingModule();
    TestBed.configureTestingModule({});
    localStorage.setItem(STORAGE_KEY, JSON.stringify(['opensky', 42, '', null]));
    expect([...createService().hidden()]).toEqual(['opensky']);
  });

  it('toggle flips a key and publishes a new set each time', () => {
    const service = createService();
    const before = service.hidden();

    service.toggle('opensky');
    expect(service.isHidden('opensky')).toBe(true);
    expect(service.hidden()).not.toBe(before);

    const mid = service.hidden();
    service.toggle('opensky');
    expect(service.isHidden('opensky')).toBe(false);
    expect(service.hidden()).not.toBe(mid);
  });

  it('hide and show are idempotent and do not emit when nothing changes', () => {
    const service = createService();
    service.hide('opensky');
    const set = service.hidden();
    service.hide('opensky');
    expect(service.hidden()).toBe(set);

    service.show('celestrak');
    expect(service.hidden()).toBe(set);
  });

  it('showAll clears every hidden key', () => {
    const service = createService();
    service.hide('a');
    service.hide('b');
    service.showAll();
    expect(service.hidden().size).toBe(0);
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it('isVisible keys entities by feedId, falling back to source, then unknown', () => {
    const service = createService();
    service.hide('opensky');
    service.hide('ADS_B');
    service.hide('unknown');

    expect(service.isVisible({ feedId: 'opensky', source: 'OPENSKY' })).toBe(false);
    expect(service.isVisible({ feedId: 'other', source: 'OPENSKY' })).toBe(true);
    expect(service.isVisible({ source: 'ADS_B' })).toBe(false);
    expect(service.isVisible({ feedId: null, source: 'AIS' })).toBe(true);
    expect(service.isVisible({})).toBe(false);

    service.show('unknown');
    expect(service.isVisible({ feedId: '', source: '' })).toBe(true);
  });

  it('keeps working when localStorage throws', () => {
    jest.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    jest.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    jest.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('blocked');
    });

    const service = createService();
    expect(service.hidden().size).toBe(0);

    service.hide('opensky');
    expect(service.isHidden('opensky')).toBe(true);
    service.toggle('opensky');
    expect(service.isHidden('opensky')).toBe(false);
    service.hide('a');
    service.showAll();
    expect(service.hidden().size).toBe(0);
  });
});
