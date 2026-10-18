import { Feed } from '../../shared/models/feed.model';
import { endpointLabel, goSnippet, jsonExample, netcatCommand, PROTO_FILES, pythonSnippet, sendTarget } from './send-guide';

const feed = (driver: string, config: Record<string, unknown>, detail: Record<string, unknown> = {}) =>
  ({ id: 'f', name: 'F', driver, config, status: { detail } }) as Feed;

describe('send guide', () => {
  it('reads listen targets from the health detail and the config', () => {
    const t = sendTarget(feed('tcp', { mode: 'listen', port: 6000, encoding: 'json', tls: { enabled: true, requireClientCert: true } }, { listen: { protocol: 'tcp', host: 'sentinel.example', port: 6000 } }))!;
    expect(t).toEqual({ mode: 'listen', protocol: 'tcp', host: 'sentinel.example', port: 6000, format: 'sentinel', encoding: 'json', tls: true, requireClientCert: true });
    expect(endpointLabel(t)).toBe('sentinel.example:6000/tcp');
    // Not running yet: the port from the config, no host.
    expect(sendTarget(feed('udp', { port: 10110 }))).toEqual(expect.objectContaining({ protocol: 'udp', host: '', port: 10110, encoding: 'protobuf' }));
    expect(sendTarget(feed('tcp', { mode: 'connect', host: 'feeds.example', port: 7000 }))).toEqual(expect.objectContaining({ mode: 'connect', host: 'feeds.example', port: 7000 }));
    expect(sendTarget(feed('opensky', {}))).toBeNull();
  });

  it('offers netcat only for plain JSON, and snippets per protocol', () => {
    const udpJson = sendTarget(feed('udp', { port: 10110, encoding: 'json' }, { listen: { host: 'h', port: 10110 } }))!;
    expect(netcatCommand(udpJson)).toBe(`echo '${jsonExample()}' | nc -u -w1 h 10110`);
    const tcpJson = { ...udpJson, protocol: 'tcp' as const };
    expect(netcatCommand(tcpJson)).toBe(`echo '${jsonExample()}' | nc -w1 h 10110`);
    expect(netcatCommand({ ...tcpJson, encoding: 'protobuf' })).toBeNull();
    expect(netcatCommand({ ...tcpJson, tls: true })).toBeNull();
    expect(JSON.parse(jsonExample()).sourceEntityId).toBeTruthy();
    expect(goSnippet({ ...tcpJson, encoding: 'protobuf' })).toContain('protodelim.MarshalTo');
    expect(goSnippet({ ...udpJson, encoding: 'protobuf' })).toContain('net.Dial("udp"');
    expect(pythonSnippet({ ...tcpJson, encoding: 'protobuf' })).toContain('_VarintBytes');
    expect(PROTO_FILES).toEqual(['observation.proto', 'common.proto', 'entity.proto', 'platform.proto']);
  });
});
