import { formatDtg } from './dtg';

describe('formatDtg', () => {
  it('formats a UTC instant as a DTG', () => {
    expect(formatDtg(new Date('2026-11-01T14:32:04Z'))).toBe('011432Z NOV 26');
  });

  it('zero-pads day, hour and minute', () => {
    expect(formatDtg(new Date('2026-03-05T04:07:00Z'))).toBe('050407Z MAR 26');
  });
});
