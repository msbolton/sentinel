import { FederationConfig } from './federation-config.entity';
import { FederationPeer } from './federation-peer.entity';
import { FederationPolicy } from './federation-policy.entity';

describe('Federation Entities', () => {
  describe('FederationConfig', () => {
    it('should create an instance with defaults', () => {
      const config = new FederationConfig();
      expect(config).toBeDefined();
      expect(config.federationEnabled).toBe(false);
    });
  });

  describe('FederationPeer', () => {
    it('should create an instance', () => {
      const peer = new FederationPeer();
      expect(peer).toBeDefined();
    });
  });

  describe('FederationPolicy', () => {
    it('should create an instance with defaults', () => {
      const policy = new FederationPolicy();
      expect(policy).toBeDefined();
      expect(policy.enabled).toBe(true);
      expect(policy.entityTypesAllowed).toEqual([]);
    });
  });
});
