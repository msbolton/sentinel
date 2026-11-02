import { isFullScreenRoute, isPageRoute, isPanelRoute, isTimelineRoute } from './route-classes';

describe('isPanelRoute', () => {
  it('is false on the bare map', () => {
    expect(isPanelRoute('/map')).toBe(false);
    expect(isPanelRoute('/map?lat=1')).toBe(false);
    expect(isPanelRoute('/')).toBe(false);
    expect(isPanelRoute('')).toBe(false);
  });

  it('is true for a right-hand panel such as alerts', () => {
    expect(isPanelRoute('/alerts')).toBe(true);
    expect(isPanelRoute('/alerts?severity=HIGH')).toBe(true);
    expect(isPanelRoute('/search')).toBe(true);
  });

  it('is false for the timeline, which is a bottom strip', () => {
    expect(isTimelineRoute('/timeline')).toBe(true);
    expect(isPanelRoute('/timeline')).toBe(false);
  });

  it('is false for full pages such as settings', () => {
    expect(isPageRoute('/settings')).toBe(true);
    expect(isPanelRoute('/settings')).toBe(false);
    expect(isPanelRoute('/connections')).toBe(false);
  });

  it('is false for the full-screen sign-in pages', () => {
    expect(isFullScreenRoute('/login')).toBe(true);
    expect(isPanelRoute('/login')).toBe(false);
  });
});
