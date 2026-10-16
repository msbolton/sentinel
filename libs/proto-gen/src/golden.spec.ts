import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  AlertEvent,
  AlertEventType,
  AlertType,
  EntityEvent,
  EntitySource,
  EntityType,
  IdentityType,
  Observation,
} from './index';

// The fixtures are written by the Go golden test
// (libs/proto-gen/src/go/sentinel/v1/golden_test.go). Decoding them here
// proves the TypeScript build reads what the Go services write.
const testdata = join(__dirname, '..', 'testdata');
const read = (name: string) => new Uint8Array(readFileSync(join(testdata, name)));

describe('proto-gen golden fixtures', () => {
  it('decodes an Observation', () => {
    const obs = Observation.decode(read('observation.bin'));
    expect(obs.observationId).toBe('3b2a7c1e-4f55-5a6b-8c9d-0e1f2a3b4c5d');
    expect(obs.feedId).toBe('opensky');
    expect(obs.source).toBe(EntitySource.OPENSKY);
    expect(obs.entityType).toBe(EntityType.AIRCRAFT);
    expect(obs.identities[0]).toEqual({ type: IdentityType.ICAO24, value: 'A1B2C3' });
    expect(obs.position?.latitude).toBeCloseTo(41.9742);
    expect(obs.position?.altitudeMeters).toBe(10668);
    expect(obs.heading).toBeCloseTo(270.5);
    expect(obs.course).toBeUndefined();
    expect(obs.sourceTimestamp).toBe('2026-10-15T12:00:00.500Z');
    expect(obs.platform?.adsb?.icaoHex).toBe('A1B2C3');
    expect(Buffer.from(obs.raw?.data ?? []).toString()).toBe('["a1b2c3","UAL123 "]');
  });

  it('decodes an EntityEvent with a position payload', () => {
    const ev = EntityEvent.decode(read('entity_event.bin'));
    expect(ev.producer).toBe('entity-service');
    expect(ev.position?.entity?.name).toBe('UAL123');
    expect(ev.position?.entity?.platforms['adsb']?.adsb?.squawk).toBe('1200');
    expect(ev.position?.observation?.feedId).toBe('opensky');
    expect(ev.created).toBeUndefined();
  });

  it('decodes an AlertEvent', () => {
    const ev = AlertEvent.decode(read('alert_event.bin'));
    expect(ev.eventType).toBe(AlertEventType.TRIGGERED);
    expect(ev.alert?.alertType).toBe(AlertType.SPEED_ANOMALY);
    expect(ev.producer).toBe('analytics-service');
    expect(ev.score).toBeCloseTo(0.93);
  });

  it('matches the JSON fixture field spelling', () => {
    const json = JSON.parse(readFileSync(join(testdata, 'observation.json'), 'utf8'));
    expect(json.source).toBe(EntitySource.OPENSKY);
    expect(json.identities[0].type).toBe(IdentityType.ICAO24);
    expect(json.position.altitudeMeters).toBe(10668);
  });
});
