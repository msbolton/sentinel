import { ENTITY_TYPE_PIN_COLORS, ENTITY_TYPE_COLORS } from '../features/map/cesium-config';
import { EntityType } from './models/entity.model';

/**
 * Normal-theme parity test (SEN-6).
 *
 * Validates that the hardcoded hex/RGB values used in Canvas, vis-network,
 * and Cesium contexts match the Normal theme token values defined in
 * styles.scss. Prevents color drift across components.
 */

/** Expected Normal-theme hex values for each entity type (must match styles.scss tokens). */
const EXPECTED_PIN_COLORS: Record<string, string> = {
  [EntityType.PERSON]: '#3b82f6',
  [EntityType.VEHICLE]: '#10b981',
  [EntityType.VESSEL]: '#06b6d4',
  [EntityType.AIRCRAFT]: '#f59e0b',
  [EntityType.FACILITY]: '#ef4444',
  [EntityType.EQUIPMENT]: '#9ca3af',
  [EntityType.UNIT]: '#10b981',
  [EntityType.SIGNAL]: '#8b5cf6',
  [EntityType.CYBER]: '#8b5cf6',
  [EntityType.SENSOR]: '#8b5cf6',
  [EntityType.UNKNOWN]: '#5a6a80',
};

/** Convert a hex color (#rrggbb) to { red, green, blue } in 0-1 range, rounded to 3 decimals. */
function hexToRgb(hex: string): { red: number; green: number; blue: number } {
  const h = hex.replace('#', '');
  return {
    red: Math.round((parseInt(h.substring(0, 2), 16) / 255) * 1000) / 1000,
    green: Math.round((parseInt(h.substring(2, 4), 16) / 255) * 1000) / 1000,
    blue: Math.round((parseInt(h.substring(4, 6), 16) / 255) * 1000) / 1000,
  };
}

describe('Theme parity – Normal baseline', () => {
  describe('ENTITY_TYPE_PIN_COLORS', () => {
    for (const type of Object.values(EntityType)) {
      it(`${type} pin color should match Normal theme`, () => {
        expect(ENTITY_TYPE_PIN_COLORS[type]).toBe(EXPECTED_PIN_COLORS[type]);
      });
    }

    it('should cover every EntityType', () => {
      const entityTypes = Object.values(EntityType);
      const pinColorKeys = Object.keys(ENTITY_TYPE_PIN_COLORS);
      for (const type of entityTypes) {
        expect(pinColorKeys).toContain(type);
      }
    });
  });

  describe('ENTITY_TYPE_COLORS (RGB)', () => {
    for (const type of Object.values(EntityType)) {
      it(`${type} RGB should convert to expected hex`, () => {
        const rgb = ENTITY_TYPE_COLORS[type];
        expect(rgb).toBeDefined();

        const expected = hexToRgb(EXPECTED_PIN_COLORS[type]);
        expect(rgb.red).toBeCloseTo(expected.red, 2);
        expect(rgb.green).toBeCloseTo(expected.green, 2);
        expect(rgb.blue).toBeCloseTo(expected.blue, 2);
        expect(rgb.alpha).toBe(1.0);
      });
    }

    it('should cover every EntityType', () => {
      const entityTypes = Object.values(EntityType);
      const rgbKeys = Object.keys(ENTITY_TYPE_COLORS);
      for (const type of entityTypes) {
        expect(rgbKeys).toContain(type);
      }
    });
  });
});
