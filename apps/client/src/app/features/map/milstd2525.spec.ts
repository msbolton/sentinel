import ms from 'milsymbol';
import { Affiliation, EntityType, TrackEnvironment } from '../../shared/models/entity.model';
import { Feed } from '../../shared/models/feed.model';
import { isFromMilitaryFeed, sidcFor, symbolDataUrl } from './milstd2525';

describe('milstd2525', () => {
  describe('sidcFor', () => {
    it('preserves an explicit 20-digit milStd2525dSymbol', () => {
      const explicit = '10065200001103000000';
      expect(
        sidcFor({
          entityType: EntityType.SIGNAL,
          affiliation: Affiliation.FRIENDLY,
          milStd2525dSymbol: explicit,
        }),
      ).toBe(explicit);
      expect(new ms.Symbol(explicit).isValid()).toBe(true);
    });

    it('maps every affiliation to the MIL-STD-2525D standard identity digit', () => {
      const cases: Array<[Affiliation | undefined, string]> = [
        [Affiliation.PENDING, '0'],
        [Affiliation.UNKNOWN, '1'],
        [undefined, '1'],
        [Affiliation.ASSUMED_FRIENDLY, '2'],
        [Affiliation.FRIENDLY, '3'],
        [Affiliation.NEUTRAL, '4'],
        [Affiliation.SUSPECT, '5'],
        [Affiliation.HOSTILE, '6'],
      ];
      for (const [affiliation, digit] of cases) {
        const sidc = sidcFor({ entityType: EntityType.UNIT, affiliation });
        expect(sidc).toHaveLength(20);
        expect(sidc[3]).toBe(digit);
        expect(new ms.Symbol(sidc).isValid()).toBe(true);
      }
    });

    it('maps SIGNAL across all SIGINT symbol sets (50-54) and emitter types with valid 2525D SIDCs', () => {
      const envs: Array<[TrackEnvironment | undefined, string]> = [
        [TrackEnvironment.SPACE, '50'],
        [TrackEnvironment.AIR, '51'],
        [TrackEnvironment.GROUND, '52'],
        [TrackEnvironment.UNKNOWN, '52'],
        [undefined, '52'],
        [TrackEnvironment.SEA_SURFACE, '53'],
        [TrackEnvironment.SUBSURFACE, '54'],
      ];
      const emitters: Array<[string | undefined, string]> = [
        ['SATCOM', '110100'],
        ['COMMS', '110100'],
        ['DATALINK', '110100'],
        ['JAMMER', '110200'],
        ['ECM', '110200'],
        ['RADAR_SEARCH', '110300'],
        ['RADAR_TRACK', '110300'],
        ['RADAR', '110300'],
        [undefined, '110000'],
        ['OTHER', '110000'],
      ];

      for (const [trackEnvironment, expectedSet] of envs) {
        for (const [emitterType, expectedCode] of emitters) {
          const sidc = sidcFor({
            entityType: EntityType.SIGNAL,
            affiliation: Affiliation.HOSTILE,
            trackEnvironment,
            platformData: emitterType ? { rf: { emitterType } } : undefined,
          });
          expect(sidc).toBe(`1006${expectedSet}0000${expectedCode}0000`);
          expect(new ms.Symbol(sidc).isValid()).toBe(true);
        }
      }
    });

    it('produces valid 2525D SIDCs for non-SIGNAL entity types and environments', () => {
      const table: Array<{
        entityType: EntityType;
        trackEnvironment?: TrackEnvironment;
        expectedSet: string;
        expectedCode: string;
      }> = [
        { entityType: EntityType.AIRCRAFT, expectedSet: '01', expectedCode: '110000' },
        { entityType: EntityType.DRONE, expectedSet: '01', expectedCode: '110300' },
        { entityType: EntityType.SATELLITE, expectedSet: '05', expectedCode: '110000' },
        { entityType: EntityType.PERSON, expectedSet: '10', expectedCode: '110000' },
        { entityType: EntityType.UNIT, expectedSet: '10', expectedCode: '110000' },
        { entityType: EntityType.VEHICLE, expectedSet: '15', expectedCode: '120000' },
        { entityType: EntityType.EQUIPMENT, expectedSet: '15', expectedCode: '220000' },
        { entityType: EntityType.SENSOR, expectedSet: '15', expectedCode: '220000' },
        { entityType: EntityType.FACILITY, expectedSet: '20', expectedCode: '110000' },
        { entityType: EntityType.VESSEL, expectedSet: '30', expectedCode: '120000' },
        { entityType: EntityType.VESSEL, trackEnvironment: TrackEnvironment.SUBSURFACE, expectedSet: '35', expectedCode: '110000' },
        { entityType: EntityType.CYBER, expectedSet: '40', expectedCode: '110000' },
        { entityType: EntityType.UNKNOWN, trackEnvironment: TrackEnvironment.AIR, expectedSet: '01', expectedCode: '110000' },
        { entityType: EntityType.UNKNOWN, trackEnvironment: TrackEnvironment.SPACE, expectedSet: '05', expectedCode: '110000' },
        { entityType: EntityType.UNKNOWN, trackEnvironment: TrackEnvironment.SEA_SURFACE, expectedSet: '30', expectedCode: '120000' },
        { entityType: EntityType.UNKNOWN, trackEnvironment: TrackEnvironment.SUBSURFACE, expectedSet: '35', expectedCode: '110000' },
        { entityType: EntityType.UNKNOWN, expectedSet: '10', expectedCode: '110000' },
      ];

      for (const row of table) {
        const sidc = sidcFor({
          entityType: row.entityType,
          affiliation: Affiliation.FRIENDLY,
          trackEnvironment: row.trackEnvironment,
        });
        expect(sidc).toBe(`1003${row.expectedSet}0000${row.expectedCode}0000`);
        expect(new ms.Symbol(sidc).isValid()).toBe(true);
      }
    });
  });

  describe('symbolDataUrl', () => {
    it('renders a non-empty SVG data URL and caches by SIDC', () => {
      const sidc = sidcFor({
        entityType: EntityType.SIGNAL,
        affiliation: Affiliation.HOSTILE,
        trackEnvironment: TrackEnvironment.SEA_SURFACE,
        platformData: { rf: { emitterType: 'RADAR_SEARCH' } },
      });
      const url1 = symbolDataUrl(sidc);
      const url2 = symbolDataUrl(sidc);
      expect(url1.startsWith('data:image/svg+xml;base64,')).toBe(true);
      expect(atob(url1.slice('data:image/svg+xml;base64,'.length))).toContain('<svg');
      expect(url1).toBe(url2);
    });
  });

  describe('isFromMilitaryFeed', () => {
    const feeds = [
      { id: 'ew-mil', config: { military: true } },
      { id: 'opensky-civ', config: {} },
    ] as unknown as Feed[];

    it('returns true only when lastFeedId or feedId matches a military connection', () => {
      expect(isFromMilitaryFeed({ lastFeedId: 'ew-mil', feedId: 'opensky-civ' }, feeds)).toBe(true);
      expect(isFromMilitaryFeed({ feedId: 'ew-mil' }, feeds)).toBe(true);
      expect(isFromMilitaryFeed({ lastFeedId: 'opensky-civ', feedId: 'ew-mil' }, feeds)).toBe(false);
      expect(isFromMilitaryFeed({ feedId: 'opensky-civ' }, feeds)).toBe(false);
      expect(isFromMilitaryFeed({}, feeds)).toBe(false);
    });
  });
});
