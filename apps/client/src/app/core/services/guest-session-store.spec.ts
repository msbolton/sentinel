import {
  clearGuestSession,
  forgetDemoVisitor,
  loadGuestSession,
  saveGuestSession,
  wasDemoVisitor,
} from './guest-session-store';

describe('guest-session-store', () => {
  afterEach(() => {
    localStorage.clear();
    jest.restoreAllMocks();
  });

  it('saves, merges and loads a guest session, and marks the browser as a demo visitor', () => {
    expect(loadGuestSession()).toBeNull();
    expect(wasDemoVisitor()).toBe(false);

    saveGuestSession({ refreshToken: 'r1' }, { replace: true });
    saveGuestSession({ guestId: '1a2b3c4d', expiresAt: '2026-10-22T13:00:00Z', visitorsFeedId: 'visitors' });
    saveGuestSession({ refreshToken: 'r2' });

    expect(loadGuestSession()).toEqual({
      refreshToken: 'r2',
      guestId: '1a2b3c4d',
      expiresAt: '2026-10-22T13:00:00Z',
      visitorsFeedId: 'visitors',
    });
    expect(wasDemoVisitor()).toBe(true);
  });

  it('replace starts from nothing: a new guest keeps none of the last one', () => {
    saveGuestSession({ refreshToken: 'old', guestId: 'old', ownEmplacementId: 'old-site' });
    saveGuestSession({ refreshToken: 'new' }, { replace: true });
    expect(loadGuestSession()).toEqual({ refreshToken: 'new' });
  });

  it('clearGuestSession drops the session but remembers the browser was a visitor; forgetDemoVisitor drops both', () => {
    saveGuestSession({ refreshToken: 'r' });
    clearGuestSession();
    expect(loadGuestSession()).toBeNull();
    expect(wasDemoVisitor()).toBe(true);

    saveGuestSession({ refreshToken: 'r' });
    forgetDemoVisitor();
    expect(loadGuestSession()).toBeNull();
    expect(wasDemoVisitor()).toBe(false);
  });

  it('ignores stored junk and fields of the wrong type', () => {
    localStorage.setItem('sentinel-demo-session', 'not json');
    expect(loadGuestSession()).toBeNull();
    localStorage.setItem('sentinel-demo-session', JSON.stringify({ refreshToken: 7, guestId: 'g', expiresAt: null }));
    expect(loadGuestSession()).toEqual({ guestId: 'g' });
  });

  it('survives storage that throws (private mode, blocked site data)', () => {
    jest.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied');
    });
    jest.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied');
    });
    jest.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('denied');
    });
    expect(() => saveGuestSession({ refreshToken: 'r' })).not.toThrow();
    expect(loadGuestSession()).toBeNull();
    expect(wasDemoVisitor()).toBe(false);
    expect(() => clearGuestSession()).not.toThrow();
    expect(() => forgetDemoVisitor()).not.toThrow();
  });
});
