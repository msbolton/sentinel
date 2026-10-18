import { Feed } from '../../shared/models/feed.model';
import {
  COUNTERS,
  FRAMING_NOTES,
  PROTO_FILES,
  SendTarget,
  endpointLabel,
  goSnippet,
  impliedFraming,
  jsonExample,
  netcatCommand,
  protobufExamplesFit,
  pythonSnippet,
  examplesNote,
  sendTarget,
  senderTime,
  tlsTestCommand,
} from './send-guide';

const feed = (driver: string, config: Record<string, unknown>, detail: Record<string, unknown> = {}) =>
  ({ id: 'f', name: 'F', driver, config, status: { detail } }) as Feed;

/** A target spelled out in full, so each test names only what it is about. */
const target = (over: Partial<SendTarget> = {}): SendTarget => ({
  mode: 'listen',
  protocol: 'tcp',
  host: 'h',
  port: 6000,
  format: 'sentinel',
  encoding: 'protobuf',
  framing: 'delimited',
  tls: false,
  requireClientCert: false,
  serverName: '',
  allow: [],
  ...over,
});

const jsonTarget = (over: Partial<SendTarget> = {}) => target({ encoding: 'json', framing: 'line', ...over });

describe('send guide', () => {
  it('reads listen targets from the health detail and the config', () => {
    const t = sendTarget(feed('tcp', { mode: 'listen', port: 6000, encoding: 'json', tls: { enabled: true, requireClientCert: true } }, { listen: { protocol: 'tcp', host: 'sentinel.example', port: 6000 } }))!;
    expect(t).toEqual({
      mode: 'listen',
      protocol: 'tcp',
      host: 'sentinel.example',
      port: 6000,
      format: 'sentinel',
      encoding: 'json',
      framing: 'line',
      tls: true,
      requireClientCert: true,
      serverName: '',
      allow: [],
    });
    expect(endpointLabel(t)).toBe('sentinel.example:6000/tcp');
    // Not running yet: the port from the config, no host.
    expect(sendTarget(feed('udp', { port: 10110 }))).toEqual(expect.objectContaining({ protocol: 'udp', host: '', port: 10110, encoding: 'protobuf' }));
    expect(sendTarget(feed('tcp', { mode: 'connect', host: 'feeds.example', port: 7000 }))).toEqual(expect.objectContaining({ mode: 'connect', host: 'feeds.example', port: 7000 }));
    expect(sendTarget(feed('opensky', {}))).toBeNull();
  });

  it('reads the allowlist and the certificate name from the config', () => {
    const t = sendTarget(feed('tcp', { mode: 'listen', port: 6000, allow: ['10.0.0.0/8', 5, '192.168.1.5'], tls: { enabled: true, serverName: 'sentinel.example' } }))!;
    expect(t.allow).toEqual(['10.0.0.0/8', '192.168.1.5']);
    expect(t.serverName).toBe('sentinel.example');
    expect(sendTarget(feed('udp', { port: 1, allow: 'anyone' }))!.allow).toEqual([]);
    // Datagrams have no TLS, whatever a stray key says.
    expect(sendTarget(feed('udp', { port: 1, tls: { enabled: true } }))!.tls).toBe(false);
    // A client certificate is asked for by a TLS listener only.
    const noTls = sendTarget(feed('tcp', { mode: 'listen', port: 1, tls: { enabled: false, requireClientCert: true } }))!;
    expect([noTls.tls, noTls.requireClientCert]).toEqual([false, false]);
    // An empty format is the default one.
    expect(sendTarget(feed('tcp', { mode: 'listen', port: 1, format: '' }))!.format).toBe('sentinel');
  });

  it('derives framing from detail, then config, then the format', () => {
    // A running connection reports what it uses.
    expect(sendTarget(feed('tcp', { mode: 'listen', port: 6000, framing: 'line' }, { framing: 'lengthprefix' }))!.framing).toBe('lengthprefix');
    expect(sendTarget(feed('udp', { port: 1 }, { framing: 'line' }))!.framing).toBe('line');
    // Not running yet: the override in the config.
    expect(sendTarget(feed('tcp', { mode: 'listen', port: 6000, framing: 'lengthprefix' }))!.framing).toBe('lengthprefix');
    expect(sendTarget(feed('tcp', { mode: 'connect', host: 'h', port: 1, format: 'nmea-ais', framing: 'lengthprefix' }))!.framing).toBe('lengthprefix');
    // UDP has no framing option; a stray key does not count.
    expect(sendTarget(feed('udp', { port: 1, framing: 'delimited' }))!.framing).toBe('message');
    // Otherwise whatever the format implies.
    expect(sendTarget(feed('tcp', { mode: 'listen', port: 6000 }))!.framing).toBe('delimited');
    expect(sendTarget(feed('tcp', { mode: 'listen', port: 6000, encoding: 'json' }))!.framing).toBe('line');
    expect(sendTarget(feed('tcp', { mode: 'listen', port: 6000, format: 'cot' }))!.framing).toBe('xmlevent');
    expect(sendTarget(feed('udp', { port: 1, format: 'cot' }))!.framing).toBe('message');

    // The same table as the server's tcp.FramingFor and udp.framingFor.
    const implied = (protocol: 'tcp' | 'udp', format: string, encoding: 'protobuf' | 'json') => impliedFraming(protocol, format, encoding);
    expect(['sentinel', ''].map((f) => implied('tcp', f, 'protobuf'))).toEqual(['delimited', 'delimited']);
    expect(['sentinel', ''].map((f) => implied('tcp', f, 'json'))).toEqual(['line', 'line']);
    expect(['cot', 'beast', 'nmea-ais', 'nmea-gps', 'sbs', 'link16', 'json'].map((f) => implied('tcp', f, 'protobuf'))).toEqual(['xmlevent', 'beast', 'line', 'line', 'line', 'line', 'line']);
    expect(['sentinel', ''].map((f) => implied('udp', f, 'protobuf'))).toEqual(['message', 'message']);
    expect(['sentinel', ''].map((f) => implied('udp', f, 'json'))).toEqual(['line', 'line']);
    expect(['cot', 'link16', 'nmea-ais', 'nmea-gps', 'sbs', 'json'].map((f) => implied('udp', f, 'protobuf'))).toEqual(['message', 'message', 'line', 'line', 'line', 'line']);
    // The encoding means something to the sentinel format only.
    expect(implied('tcp', 'cot', 'json')).toBe('xmlevent');
    expect(implied('udp', 'cot', 'json')).toBe('message');
  });

  it('describes every framing the server has', () => {
    expect(Object.keys(FRAMING_NOTES).sort()).toEqual(['beast', 'delimited', 'lengthprefix', 'line', 'message', 'xmlevent']);
    for (const note of Object.values(FRAMING_NOTES)) {
      expect(note.length).toBeGreaterThan(10);
    }
  });

  it('fits protobuf examples only to delimited TCP and whole-datagram UDP', () => {
    expect(protobufExamplesFit(target())).toBe(true);
    expect(protobufExamplesFit(target({ tls: true, requireClientCert: true }))).toBe(true);
    expect(protobufExamplesFit(target({ protocol: 'udp', framing: 'message' }))).toBe(true);
    // The examples write a protobuf varint length, or a whole datagram: nothing else.
    expect(protobufExamplesFit(target({ framing: 'lengthprefix' }))).toBe(false);
    expect(protobufExamplesFit(target({ framing: 'line' }))).toBe(false);
    expect(protobufExamplesFit(target({ protocol: 'udp', framing: 'line' }))).toBe(false);
    expect(protobufExamplesFit(target({ protocol: 'udp', framing: 'delimited' }))).toBe(false);
    expect(protobufExamplesFit(target({ protocol: 'tcp', framing: 'message' }))).toBe(false);
    // JSON and the device formats are not Sentinel's protobuf.
    expect(protobufExamplesFit(jsonTarget())).toBe(false);
    expect(protobufExamplesFit(target({ format: 'cot', framing: 'xmlevent' }))).toBe(false);
  });

  it('says why a connection has no Go or Python examples', () => {
    expect(examplesNote(target())).toBe('');
    expect(examplesNote(target({ protocol: 'udp', framing: 'message' }))).toBe('');
    expect(examplesNote(jsonTarget())).toMatch(/binary protobuf/);
    expect(examplesNote(target({ framing: 'lengthprefix' }))).toContain('lengthprefix');
    expect(examplesNote(target({ framing: 'lengthprefix' }))).toContain(FRAMING_NOTES['lengthprefix']);
    const device = examplesNote(target({ format: 'nmea-gps', framing: 'line', port: 10110, protocol: 'udp' }));
    expect(device).toContain('nmea-gps');
    expect(device).toContain('h:10110/udp');
  });

  it('offers netcat only for plain JSON lines, and snippets per protocol', () => {
    const udpJson = jsonTarget({ protocol: 'udp', host: 'h', port: 10110 });
    expect(netcatCommand(udpJson)).toBe(`echo '${jsonExample()}' | nc -u -w1 h 10110`);
    const tcpJson = jsonTarget({ port: 10110 });
    expect(netcatCommand(tcpJson)).toBe(`echo '${jsonExample()}' | nc -w1 h 10110`);
    // A listener that has not started has no host yet: say where it goes.
    expect(netcatCommand({ ...tcpJson, host: '' })).toBe(`echo '${jsonExample()}' | nc -w1 LISTENER_HOST 10110`);
    expect(netcatCommand({ ...tcpJson, encoding: 'protobuf', framing: 'delimited' })).toBeNull();
    expect(netcatCommand({ ...tcpJson, tls: true })).toBeNull();
    // A line is only a message when the framing says so.
    expect(netcatCommand({ ...tcpJson, framing: 'lengthprefix' })).toBeNull();
    expect(netcatCommand({ ...tcpJson, format: 'cot', framing: 'xmlevent' })).toBeNull();
    expect(netcatCommand({ ...tcpJson, mode: 'connect' })).toBeNull();
    expect(JSON.parse(jsonExample()).sourceEntityId).toBeTruthy();
    expect(goSnippet(target())).toContain('protodelim.MarshalTo');
    expect(goSnippet(target({ protocol: 'udp', framing: 'message' }))).toContain('net.Dial("udp"');
    expect(pythonSnippet(target())).toContain('varint(len(data)) + data');
    expect(PROTO_FILES).toEqual(['observation.proto', 'common.proto', 'entity.proto', 'platform.proto']);
  });

  it('puts an IPv6 host in brackets where an address is written', () => {
    expect(endpointLabel(target({ host: '2001:db8::1' }))).toBe('[2001:db8::1]:6000/tcp');
    expect(endpointLabel(target({ host: '' }))).toBe('listener:6000/tcp');
    expect(goSnippet(target({ host: '2001:db8::1' }))).toContain('net.Dial("tcp", "[2001:db8::1]:6000")');
    expect(tlsTestCommand(jsonTarget({ tls: true, host: '2001:db8::1' }))).toContain('-connect [2001:db8::1]:6000 -servername 2001:db8::1');
  });

  describe('Go', () => {
    /** The paths an import block lists, aliases dropped. */
    const importsOf = (src: string): string[] => {
      const block = /^import \(\n([\s\S]*?)\n\)/m.exec(src)?.[1] ?? '';
      return block
        .split('\n')
        .map((line) => line.trim().replace(/^\w+\s+/, '').replace(/"/g, ''))
        .filter(Boolean);
    };
    const body = (src: string) => src.slice(src.indexOf('func main()'));
    const PB = 'google.golang.org/protobuf';
    const SENTINEL = 'example.com/yourapp/sentinel/v1';

    it('writes a complete Go program per transport', () => {
      const tcp = goSnippet(target());
      expect(tcp).toContain('package main');
      expect(tcp).toContain('func main() {');
      expect(tcp).toContain('protoc --go_out=. --go_opt=paths=source_relative sentinel/v1/*.proto');
      expect(tcp).toContain(`import sentinelv1 "${SENTINEL}"`);
      expect(importsOf(tcp)).toEqual(['log', 'net', `${PB}/encoding/protodelim`, SENTINEL]);
      expect(tcp).toContain('net.Dial("tcp", "h:6000")');
      expect(tcp).toContain('protodelim.MarshalTo(conn, obs)');
      expect(tcp).not.toContain('tls.');

      const udp = goSnippet(target({ protocol: 'udp', framing: 'message' }));
      expect(importsOf(udp)).toEqual(['log', 'net', `${PB}/proto`, SENTINEL]);
      expect(udp).toContain('net.Dial("udp", "h:6000")');
      // proto.Marshal's error is checked, not thrown away.
      expect(udp).toMatch(/data, err := proto\.Marshal\(obs\)\n\tif err != nil \{\n\t\tlog\.Fatal\(err\)/);
      expect(udp).not.toContain('protodelim');

      const tls = goSnippet(target({ tls: true }));
      expect(importsOf(tls)).toEqual(['crypto/tls', 'crypto/x509', 'log', 'os', `${PB}/encoding/protodelim`, SENTINEL]);
      expect(tls).not.toContain('"net"');
      expect(tls).toContain('tls.Dial("tcp", "h:6000", cfg)');
      expect(tls).toContain('ServerName: "h"');
      expect(tls).toContain('os.ReadFile("ca.pem")');
      expect(tls).toContain('AppendCertsFromPEM(ca)');
      expect(tls).toContain('protodelim.MarshalTo(conn, obs)');
    });

    it('loads a client certificate only when the listener requires one', () => {
      expect(goSnippet(target({ tls: true }))).not.toContain('LoadX509KeyPair');
      expect(goSnippet(target({ tls: true }))).not.toContain('cfg.Certificates');
      const mtls = goSnippet(target({ tls: true, requireClientCert: true }));
      expect(mtls).toContain('tls.LoadX509KeyPair("client.pem", "client.key")');
      expect(mtls).toContain('cfg.Certificates = []tls.Certificate{cert}');
      // The certificate is loaded before the dial that presents it.
      expect(mtls.indexOf('LoadX509KeyPair')).toBeLessThan(mtls.indexOf('tls.Dial'));
    });

    it('uses every import it declares and none it leaves out', () => {
      const variants = [
        target(),
        target({ tls: true }),
        target({ tls: true, requireClientCert: true }),
        target({ protocol: 'udp', framing: 'message' }),
        target({ host: '' }),
      ];
      for (const t of variants) {
        const src = goSnippet(t);
        // A program, not a fragment: there is something to compare.
        expect(src).toContain('func main()');
        expect(importsOf(src).length).toBeGreaterThan(2);
        // Selectors in code only: not words in a comment or a file name in a string.
        const code = body(src).replace(/\/\/.*$/gm, '').replace(/"[^"]*"/g, '""');
        const used = new Set(Array.from(code.matchAll(/\b([a-z][a-z0-9]*)\./g), (m) => m[1]));
        const qualifier = (path: string) => (path === SENTINEL ? 'sentinelv1' : path.split('/').pop()!);
        const declared = new Set(importsOf(src).map(qualifier));
        // A selector on a local value (conn.Close, cfg.Certificates) is not a package.
        const locals = new Set(['conn', 'cfg', 'roots', 'obs']);
        const packages = new Set([...used].filter((name) => !locals.has(name)));
        expect([...declared].sort()).toEqual([...packages].sort());
      }
    });

    it('names the certificate it expects, and a placeholder while the host is unknown', () => {
      expect(goSnippet(target({ host: '' }))).toContain('net.Dial("tcp", "LISTENER_HOST:6000")');
      const named = goSnippet(target({ tls: true, serverName: 'sentinel.example' }));
      expect(named).toContain('ServerName: "sentinel.example"');
      expect(named).toContain('tls.Dial("tcp", "h:6000", cfg)');
      expect(goSnippet(target({ tls: true, host: '' }))).toContain('ServerName: "LISTENER_HOST"');
    });

    it('is laid out as gofmt does: tabs for indents, no doc comment before the package clause', () => {
      for (const src of [goSnippet(target()), goSnippet(target({ tls: true, requireClientCert: true })), goSnippet(target({ protocol: 'udp', framing: 'message' }))]) {
        expect(src).not.toMatch(/^ +\S/m);
        expect(src).toMatch(/\n\n(?=package main\n)/);
        expect(src).toContain('\n\tobs := &sentinelv1.Observation{\n\t\tSourceEntityId: "example-1",\n\t\tEntityType:     sentinelv1.EntityType_ENTITY_TYPE_VEHICLE,');
      }
    });
  });

  describe('Python', () => {
    it('Python uses its own varint and ssl for TLS', () => {
      const plain = pythonSnippet(target());
      expect(plain).not.toContain('_VarintBytes');
      // A runtime older than the protoc that wrote the code refuses to load it.
      expect(plain).toContain('pip install -U protobuf');
      expect(plain).toContain('def varint(n):');
      expect(plain).toContain('from sentinel.v1 import entity_pb2, observation_pb2');
      expect(plain).toContain('entity_type=entity_pb2.ENTITY_TYPE_VEHICLE');
      expect(plain).toContain('socket.create_connection(("h", 6000))');
      expect(plain).not.toContain('ssl');

      const tls = pythonSnippet(target({ tls: true }));
      expect(tls).toContain('import ssl');
      // Closing a TLS 1.3 connection at once can reset it and lose the message; plain TCP needs no wait.
      expect(tls).toContain('time.sleep(0.5)');
      expect(tls).toContain('import time');
      expect(plain).not.toContain('time.sleep');
      expect(pythonSnippet(target({ tls: true, requireClientCert: true }))).toContain('time.sleep(0.5)');
      expect(tls).toContain('ssl.create_default_context(cafile="ca.pem")');
      expect(tls).toContain('ctx.wrap_socket(raw, server_hostname="h")');
      expect(tls).toContain('def varint(n):');
      expect(tls).not.toContain('load_cert_chain');

      const mtls = pythonSnippet(target({ tls: true, requireClientCert: true }));
      expect(mtls).toContain('ctx.load_cert_chain("client.pem", "client.key")');
      expect(mtls.indexOf('load_cert_chain')).toBeLessThan(mtls.indexOf('wrap_socket'));

      expect(pythonSnippet(target({ tls: true, serverName: 'sentinel.example' }))).toContain('server_hostname="sentinel.example"');
      expect(pythonSnippet(target({ host: '' }))).toContain('("LISTENER_HOST", 6000)');
    });

    it('sends a UDP datagram as one whole message with no length prefix', () => {
      const udp = pythonSnippet(target({ protocol: 'udp', framing: 'message', port: 10110 }));
      expect(udp).toContain('socket.SOCK_DGRAM');
      expect(udp).toContain('s.sendto(data, ("h", 10110))');
      expect(udp).not.toContain('varint');
      expect(udp).not.toContain('ssl');
    });
  });

  it('tests TLS JSON with openssl', () => {
    const base = `echo '${jsonExample()}' | openssl s_client -quiet -no_ign_eof -verify_return_error -connect h:6000 -servername h -CAfile ca.pem`;
    expect(tlsTestCommand(jsonTarget({ tls: true }))).toBe(base);
    // Client certificate flags only when the listener asks for one.
    expect(tlsTestCommand(jsonTarget({ tls: true, requireClientCert: true }))).toBe(`${base} -cert client.pem -key client.key`);
    expect(tlsTestCommand(jsonTarget({ tls: true, host: '' }))).toContain('-connect LISTENER_HOST:6000 -servername LISTENER_HOST');
    expect(tlsTestCommand(jsonTarget({ tls: true, serverName: 'sentinel.example' }))).toContain('-connect h:6000 -servername sentinel.example');
    // Only JSON lines over TCP with TLS.
    expect(tlsTestCommand(jsonTarget({ tls: true, protocol: 'udp' }))).toBeNull();
    expect(tlsTestCommand(target({ tls: true }))).toBeNull();
    expect(tlsTestCommand(jsonTarget({ tls: true, format: 'nmea-gps' }))).toBeNull();
    expect(tlsTestCommand(jsonTarget({ tls: true, framing: 'lengthprefix' }))).toBeNull();
    expect(tlsTestCommand(jsonTarget())).toBeNull();
    expect(tlsTestCommand(jsonTarget({ tls: true, mode: 'connect' }))).toBeNull();
  });

  describe('counters', () => {
    const keysFor = (t: SendTarget) => COUNTERS.filter((c) => c.applies(t)).map((c) => c.key);

    it('names and explains every counter, in plain words', () => {
      expect(Object.fromEntries(COUNTERS.map((c) => [c.key, c.label]))).toEqual({
        accepted: 'Accepted',
        rejected: 'Not allowed',
        refused: 'Over connection limit',
        rateLimited: 'Rate limited',
        senderRateLimited: 'Rate limited (per sender)',
        oversize: 'Too large',
        kernelDropped: 'Dropped by the OS',
        tlsFailed: 'TLS failed',
        tlsReloadFailed: 'TLS reload failed',
        malformed: 'Undecodable',
        messages: 'Messages',
        reconnects: 'Reconnects',
      });
      expect(new Set(COUNTERS.map((c) => c.key)).size).toBe(COUNTERS.length);
      for (const c of COUNTERS) {
        expect(c.help.length).toBeGreaterThan(20);
      }
    });

    it('applies each counter to the connections that can raise it', () => {
      // TCP listener: everything except what only UDP, TLS or a dialling connection has.
      expect(keysFor(target())).toEqual(['accepted', 'rejected', 'refused', 'rateLimited', 'senderRateLimited', 'oversize', 'tlsFailed', 'malformed']);
      expect(keysFor(target({ tls: true }))).toEqual(['accepted', 'rejected', 'refused', 'rateLimited', 'senderRateLimited', 'oversize', 'tlsFailed', 'tlsReloadFailed', 'malformed']);
      // UDP never refuses a connection or speaks TLS; the OS may drop its datagrams.
      expect(keysFor(target({ protocol: 'udp', framing: 'message' }))).toEqual(['accepted', 'rejected', 'rateLimited', 'senderRateLimited', 'oversize', 'kernelDropped', 'malformed']);
      // A connection Sentinel dials out has no listener counters.
      expect(keysFor(target({ mode: 'connect' }))).toEqual(['malformed', 'messages', 'reconnects']);
    });
  });

  it('tells a time from the zero time Go sends for "never"', () => {
    expect(senderTime('2026-10-18T10:00:05Z')).toBe('2026-10-18T10:00:05Z');
    expect(senderTime('0001-01-01T00:00:00Z')).toBeNull();
    expect(senderTime('1970-01-01T00:00:00Z')).toBeNull();
    expect(senderTime('')).toBeNull();
    expect(senderTime(undefined)).toBeNull();
    expect(senderTime(null)).toBeNull();
    expect(senderTime('yesterday')).toBeNull();
  });
});
