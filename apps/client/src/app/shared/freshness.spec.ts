import { freshnessOf } from './freshness';

describe('freshnessOf', () => {
  const now = Date.parse('2026-10-17T14:32:20Z');
  const at = (secondsAgo: number) => new Date(now - secondsAgo * 1000).toISOString();

  it('is fresh under a minute, in seconds', () => {
    expect(freshnessOf({ lastSeenAt: at(12), updatedAt: at(12) }, now)).toEqual({ state: 'fresh', label: '12 s ago' });
  });

  it('is aging under ten minutes, in minutes', () => {
    expect(freshnessOf({ lastSeenAt: at(4 * 60 + 5), updatedAt: at(4 * 60 + 5) }, now)).toEqual({ state: 'aging', label: '4 min ago' });
  });

  it('is stale from ten minutes, in hours and then days', () => {
    expect(freshnessOf({ lastSeenAt: at(10 * 60), updatedAt: at(10 * 60) }, now)).toEqual({ state: 'stale', label: '10 min ago' });
    expect(freshnessOf({ lastSeenAt: at(2 * 3600 + 100), updatedAt: at(1) }, now)).toEqual({ state: 'stale', label: '2 h ago' });
    expect(freshnessOf({ lastSeenAt: at(3 * 86400), updatedAt: at(1) }, now)).toEqual({ state: 'stale', label: '3 d ago' });
  });

  it('falls back to the update time when there is no last-seen time', () => {
    expect(freshnessOf({ updatedAt: at(30) }, now)).toEqual({ state: 'fresh', label: '30 s ago' });
  });

  it('trusts the age-out state over the clock', () => {
    expect(freshnessOf({ lastSeenAt: at(5), updatedAt: at(5), ageoutState: 'STALE' }, now)).toEqual({ state: 'stale', label: '5 s ago' });
    expect(freshnessOf({ lastSeenAt: at(5), updatedAt: at(5), ageoutState: 'AGED_OUT' }, now)).toEqual({ state: 'stale', label: 'aged out' });
  });

  it('never reports a negative age for a clock that runs ahead', () => {
    expect(freshnessOf({ lastSeenAt: at(-40), updatedAt: at(-40) }, now)).toEqual({ state: 'fresh', label: '0 s ago' });
  });
});
