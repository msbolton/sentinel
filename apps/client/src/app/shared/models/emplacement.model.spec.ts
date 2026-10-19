import { draftOf, emplacementIdOf, inputOf, newDraft, Emplacement, coverageSummary } from './emplacement.model';

const stored: Emplacement = {
  id: 'e1', name: 'Coastal', description: '', mobility: 'FIXED', sensorType: 'RADAR',
  position: { lat: 50, lon: -4 }, altitude: null, boresightDeg: 270, azimuthReference: 'TRUE_NORTH',
  coverage: { minRangeM: 0, maxRangeM: 60000, azimuthWidthDeg: 120 }, defaultSigma: {},
  feedId: null, classification: 'UNCLASSIFIED', exercise: false, active: true,
  lastReportAt: null, createdBy: 'u', createdAt: '', updatedAt: '', version: 3,
};

describe('emplacement model', () => {
  it('finds the emplacement id of an emplacement entity, and nothing on other entities', () => {
    expect(emplacementIdOf({ identities: [{ type: 'CALLSIGN', value: 'R1' }, { type: 'EMPLACEMENT', value: 'e1' }] })).toBe('e1');
    expect(emplacementIdOf({ identities: [{ type: 'ICAO24', value: 'ABC123' }] })).toBeNull();
    expect(emplacementIdOf({})).toBeNull();
  });

  it('turns a stored emplacement into a draft and back into an input', () => {
    const d = draftOf(stored);
    expect(d.id).toBe('e1');
    expect(d.version).toBe(3);
    expect(newDraft().version).toBeNull();
    const input = inputOf({ ...d, feedId: null });
    expect(input.feedId).toBe(''); // "" unbinds on the server
    expect(input.position).toEqual({ lat: 50, lon: -4 });
    expect('id' in input).toBe(false);
    expect('version' in input).toBe(false);
  });

  it('sends no coverage as all zeros', () => {
    const input = inputOf({ ...newDraft(), coverage: { minRangeM: 0, maxRangeM: 0, azimuthWidthDeg: 120 } });
    expect(input.coverage).toEqual({ minRangeM: 0, maxRangeM: 0, azimuthWidthDeg: 0 });
  });

  it('summarises coverage', () => {
    expect(coverageSummary(stored.coverage)).toBe('120° · 60 km');
    expect(coverageSummary({ minRangeM: 0, maxRangeM: 5000, azimuthWidthDeg: 360 })).toBe('omni · 5 km');
    expect(coverageSummary({ minRangeM: 0, maxRangeM: 0, azimuthWidthDeg: 0 })).toBe('no coverage');
  });
});
