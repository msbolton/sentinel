import { FederationOverlayService } from './federation-overlay.service';

describe('FederationOverlayService', () => {
  let service: FederationOverlayService;

  beforeEach(() => {
    service = new FederationOverlayService();
  });

  it('should be created', () => {
    expect(service).toBeTruthy();
  });

  describe('isFederatedEntity', () => {
    it('should return true for entities with sourceInstanceId', () => {
      expect(service.isFederatedEntity({ sourceInstanceId: 'peer-1' } as any)).toBe(true);
    });

    it('should return false for local entities', () => {
      expect(service.isFederatedEntity({} as any)).toBe(false);
      expect(service.isFederatedEntity({ sourceInstanceId: undefined } as any)).toBe(false);
    });
  });

  describe('formatFederatedLabel', () => {
    it('should append source badge to entity name', () => {
      const result = service.formatFederatedLabel('HAWK-9', 'BRAVO');
      expect(result).toBe('HAWK-9 [BRAVO]');
    });

    it('should return plain name for local entities', () => {
      const result = service.formatFederatedLabel('UAV-307', undefined);
      expect(result).toBe('UAV-307');
    });
  });

  describe('hexToRgb', () => {
    it('should parse hex color to RGB object', () => {
      const rgb = service.hexToRgb('#f97316');
      expect(rgb).toEqual({ r: 249, g: 115, b: 22 });
    });

    it('should return null for invalid hex', () => {
      expect(service.hexToRgb('invalid')).toBeNull();
    });
  });
});
