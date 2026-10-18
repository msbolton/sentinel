import { connectionHints, nextFreePort, PORT_BASE } from './connection-hints';
import { ListenPorts } from '../../shared/models/feed.model';

/**
 * What `GET /api/v1/feeds/ports` answers. Its suggestions differ from what the form would work out for itself
 * (6001 and 6002 are taken), so a test that offers one shows the server's was used.
 */
const ports: ListenPorts = {
  min: 1024,
  max: 65535,
  reserved: [
    { from: 3000, to: 3005 },
    { from: 5432, to: 5432 },
  ],
  taken: [
    { protocol: 'tcp', port: 6001, feedId: 'radar', feedName: 'Radar feed', enabled: true },
    { protocol: 'tcp', port: 6002, feedId: 'gate', feedName: 'Gate feed', enabled: false },
    { protocol: 'udp', port: 10110, feedId: 'pier', feedName: 'Pier AIS', enabled: false },
  ],
  suggested: { tcp: 6500, udp: 10111 },
};

const allow = ['10.0.0.0/8'];

describe('connectionHints', () => {
  it('warns about an empty allowlist on a TCP listener and a UDP connection only', () => {
    const allowHints = (driverId: string | undefined, config: Record<string, unknown>) =>
      connectionHints({ driverId, config, ports: null }).filter((h) => h.path.endsWith('/allow'));

    expect(allowHints('tcp', { mode: 'listen', port: 6100 })).toEqual([
      {
        path: '/config/allow',
        level: 'warn',
        message: "Empty: anyone who can reach port 6100 can connect. List the senders' IP addresses or CIDR ranges.",
      },
    ]);
    expect(allowHints('udp', { port: 6100 })).toEqual([
      {
        path: '/config/allow',
        level: 'warn',
        message:
          "Empty: anyone who can reach port 6100 can connect. List the senders' IP addresses or CIDR ranges. " +
          'UDP source addresses can be forged; on an untrusted network prefer TCP with mutual TLS.',
      },
    ]);

    // an allowlist is the answer
    expect(allowHints('tcp', { mode: 'listen', port: 6100, allow })).toEqual([]);
    expect(allowHints('udp', { port: 6100, allow: ['192.0.2.7'] })).toEqual([]);
    // a connection that dials out, or does not listen at all, has no port to leave open
    expect(allowHints('tcp', { mode: 'connect', host: 'radar.example', port: 7000 })).toEqual([]);
    expect(allowHints('tcp', { port: 6100 })).toEqual([]);
    expect(allowHints('opensky', {})).toEqual([]);
    expect(allowHints('stream', { transport: { kind: 'tcpclient', address: 'radar.example:4001' } })).toEqual([]);
    expect(allowHints(undefined, { port: 6100 })).toEqual([]);
  });

  it('names no port in the allowlist warning while there is none', () => {
    const [warning] = connectionHints({ driverId: 'tcp', config: { mode: 'listen' }, ports: null });

    expect(warning.message).toBe(
      "Empty: anyone who can reach the port can connect. List the senders' IP addresses or CIDR ranges.",
    );
  });

  it('flags a reserved port and offers the next free one', () => {
    expect(connectionHints({ driverId: 'tcp', config: { mode: 'listen', port: 3002, allow }, ports })).toEqual([
      {
        path: '/config/port',
        level: 'warn',
        message: "Port 3002 is one of Sentinel's own (INGEST_RESERVED_PORTS).",
        action: { label: 'Use 3006', value: 3006 },
      },
    ]);
    // a single reserved port is a range of one; the edges of a range are in it
    expect(connectionHints({ driverId: 'udp', config: { port: 5432, allow }, ports })[0].action).toEqual({ label: 'Use 5433', value: 5433 });
    expect(connectionHints({ driverId: 'udp', config: { port: 3000, allow }, ports })[0].action?.value).toBe(3006);
    expect(connectionHints({ driverId: 'udp', config: { port: 3005, allow }, ports })[0].action?.value).toBe(3006);
    expect(connectionHints({ driverId: 'udp', config: { port: 3006, allow }, ports })).toEqual([]);
    expect(connectionHints({ driverId: 'udp', config: { port: 2999, allow }, ports })).toEqual([]);

    // reserved wins over taken: the server says so first
    const both: ListenPorts = { ...ports, taken: [{ protocol: 'tcp', port: 3002, feedId: 'radar', feedName: 'Radar feed', enabled: true }] };
    const [hint, ...rest] = connectionHints({ driverId: 'tcp', config: { mode: 'listen', port: 3002, allow }, ports: both });
    expect(hint.message).toContain("one of Sentinel's own");
    expect(rest).toEqual([]);
  });

  it('flags a port another connection of the same protocol uses, not its own', () => {
    const tcp = (config: Record<string, unknown>, selfId?: string) =>
      connectionHints({ driverId: 'tcp', config: { mode: 'listen', allow, ...config }, ports, selfId });

    // the next free port skips the one a disabled connection keeps
    expect(tcp({ port: 6001 })).toEqual([
      {
        path: '/config/port',
        level: 'warn',
        message: 'Port 6001/tcp is already used by Radar feed (radar).',
        action: { label: 'Use 6003', value: 6003 },
      },
    ]);
    // a disabled connection keeps its port, and the hint says so
    expect(tcp({ port: 6002 })[0].message).toBe(
      'Port 6002/tcp is already used by Gate feed (gate). Disabled connections keep their port.',
    );
    expect(connectionHints({ driverId: 'udp', config: { port: 10110, allow }, ports })).toEqual([
      {
        path: '/config/port',
        level: 'warn',
        message: 'Port 10110/udp is already used by Pier AIS (pier). Disabled connections keep their port.',
        action: { label: 'Use 10111', value: 10111 },
      },
    ]);

    // the connection being edited is not its own neighbour
    expect(tcp({ port: 6001 }, 'radar')).toEqual([]);
    expect(tcp({ port: 6002 }, 'radar')[0].message).toContain('Gate feed (gate)');
    // and its port is free for the next suggestion
    expect(tcp({ port: 6001, mode: 'listen' }, 'gate')[0].action?.value).toBe(6002);

    // a TCP and a UDP connection may share a number
    expect(tcp({ port: 10110 })).toEqual([]);
    expect(connectionHints({ driverId: 'udp', config: { port: 6001, allow }, ports })).toEqual([]);
  });

  it('suggests a free port when none is set', () => {
    expect(connectionHints({ driverId: 'tcp', config: { mode: 'listen', allow }, ports })).toEqual([
      {
        path: '/config/port',
        level: 'info',
        message: 'Port 6500 is not used by Sentinel or another TCP connection.',
        action: { label: 'Use 6500', value: 6500 },
      },
    ]);
    expect(connectionHints({ driverId: 'udp', config: { allow }, ports })).toEqual([
      {
        path: '/config/port',
        level: 'info',
        message: 'Port 10111 is not used by Sentinel or another UDP connection.',
        action: { label: 'Use 10111', value: 10111 },
      },
    ]);
  });

  it('works out a suggestion itself when the server offers none that is free', () => {
    const suggest = (suggested: ListenPorts['suggested']) =>
      connectionHints({ driverId: 'tcp', config: { mode: 'listen', allow }, ports: { ...ports, suggested } })[0].action?.value;

    // from the base, past what is taken
    expect(PORT_BASE).toBe(6001);
    expect(suggest({})).toBe(6003);
    expect(suggest({ udp: 10111 })).toBe(6003);
    // nothing usable: zero (none free), not a number, out of range, reserved, taken
    for (const bad of [0, -1, 1.5, 80, 70000, 3002, 6001, '6500', null]) {
      expect(suggest({ tcp: bad as unknown as number })).toBe(6003);
    }
  });

  it('shows no port hint without an answer from the server, and keeps the allowlist warning', () => {
    for (const unanswered of [null, undefined]) {
      const hints = connectionHints({ driverId: 'tcp', config: { mode: 'listen' }, ports: unanswered });
      expect(hints.map((h) => h.path)).toEqual(['/config/allow']);
    }
    // and no hint breaks on an answer that is not what was promised
    const odd = { min: 1024, max: 65535 } as unknown as ListenPorts;
    expect(connectionHints({ driverId: 'tcp', config: { mode: 'listen', port: 6001 }, ports: odd }).map((h) => h.path)).toEqual([
      '/config/allow',
    ]);
    const garbled = { ...odd, reserved: 'none', taken: {} } as unknown as ListenPorts;
    expect(connectionHints({ driverId: 'tcp', config: { mode: 'listen' }, ports: garbled }).map((h) => h.path)).toEqual([
      '/config/port',
      '/config/allow',
    ]);
  });

  it('says nothing about a free port, or about one the schema already refuses', () => {
    const portHints = (driverId: string, config: Record<string, unknown>) =>
      connectionHints({ driverId, config: { allow, ...config }, ports }).filter((h) => h.path === '/config/port');

    expect(portHints('tcp', { mode: 'listen', port: 6100 })).toEqual([]);
    expect(portHints('udp', { port: 10200 })).toEqual([]);
    // the port field of tcp and udp has its own minimum and maximum
    expect(portHints('tcp', { mode: 'listen', port: 80 })).toEqual([]);
    expect(portHints('udp', { port: 70000 })).toEqual([]);
  });

  it('handles a stream listener by its address', () => {
    const stream = (transport: Record<string, unknown>, selfId?: string) =>
      connectionHints({ driverId: 'stream', config: { transport: { allow, ...transport } }, ports, selfId });
    const address = '/config/transport/address';

    // taken: the action keeps the interface and replaces the port
    expect(stream({ kind: 'tcpserver', address: ':6001' })).toEqual([
      {
        path: address,
        level: 'warn',
        message: 'Port 6001/tcp is already used by Radar feed (radar).',
        action: { label: 'Use 6003', value: ':6003' },
      },
    ]);
    expect(stream({ kind: 'tcpserver', address: '10.0.0.5:6001' })[0].action?.value).toBe('10.0.0.5:6003');
    expect(stream({ kind: 'tcpserver', address: '[::]:6001' })[0].action?.value).toBe('[::]:6003');
    expect(stream({ kind: 'tcpserver', address: ':6001' }, 'radar')).toEqual([]);

    // udp has its own ports
    expect(stream({ kind: 'udp', address: '0.0.0.0:10110' })[0]).toEqual(
      expect.objectContaining({ path: address, action: { label: 'Use 10111', value: '0.0.0.0:10111' } }),
    );
    expect(stream({ kind: 'udp', address: ':6001' })).toEqual([]);

    // reserved
    expect(stream({ kind: 'tcpserver', address: ':3001' })[0]).toEqual(
      expect.objectContaining({ message: "Port 3001 is one of Sentinel's own (INGEST_RESERVED_PORTS).", action: { label: 'Use 3006', value: ':3006' } }),
    );

    // no port yet: an address with a host keeps it
    expect(stream({ kind: 'tcpserver' })).toEqual([
      {
        path: address,
        level: 'info',
        message: 'Port 6500 is not used by Sentinel or another TCP connection.',
        action: { label: 'Use 6500', value: ':6500' },
      },
    ]);
    expect(stream({ kind: 'udp', address: 'localhost' })[0].action?.value).toBe('localhost:10111');
    expect(stream({ kind: 'tcpserver', address: '[::1]' })[0].action?.value).toBe('[::1]:6500');

    // the address is free text, so the floor and the ceiling are the hint's to point out
    expect(stream({ kind: 'tcpserver', address: ':80' })).toEqual([{ path: address, level: 'warn', message: 'Listening ports start at 1024.' }]);
    expect(stream({ kind: 'udp', address: ':70000' })).toEqual([{ path: address, level: 'warn', message: 'Listening ports end at 65535.' }]);
    expect(stream({ kind: 'tcpserver', address: ':1024' })).toEqual([]);

    // its allowlist is inside the transport, and a stream that dials out has none
    expect(connectionHints({ driverId: 'stream', config: { transport: { kind: 'tcpserver', address: ':6100' } }, ports })).toEqual([
      {
        path: '/config/transport/allow',
        level: 'warn',
        message: "Empty: anyone who can reach port 6100 can connect. List the senders' IP addresses or CIDR ranges.",
      },
    ]);
    expect(
      connectionHints({ driverId: 'stream', config: { transport: { kind: 'udp', address: ':6100' } }, ports })[0].message,
    ).toContain('UDP source addresses can be forged');
    expect(stream({ kind: 'tcpclient', address: ':6001' })).toEqual([]);
    expect(connectionHints({ driverId: 'stream', config: {}, ports })).toEqual([]);
  });

  it('uses the limits the server reports', () => {
    const narrow: ListenPorts = { ...ports, min: 2000, max: 2100, reserved: [], taken: [], suggested: {} };

    expect(connectionHints({ driverId: 'stream', config: { transport: { kind: 'udp', address: ':1500', allow } }, ports: narrow })).toEqual([
      { path: '/config/transport/address', level: 'warn', message: 'Listening ports start at 2000.' },
    ]);
    // the fallback suggestion starts at the base, and wraps to the floor when that is above the ceiling
    expect(connectionHints({ driverId: 'udp', config: { allow }, ports: narrow })[0].action?.value).toBe(2000);
  });
});

describe('nextFreePort', () => {
  it('skips ranges and taken ports and wraps', () => {
    expect(nextFreePort(6001, [], [])).toBe(6001);
    expect(nextFreePort(6001, [], [6001, 6002])).toBe(6003);
    // the ends of a range are inside it
    expect(nextFreePort(6000, [{ from: 6000, to: 6010 }], [])).toBe(6011);
    expect(nextFreePort(6010, [{ from: 6000, to: 6010 }], [])).toBe(6011);
    // ranges and taken ports in turn
    expect(nextFreePort(6000, [{ from: 6000, to: 6010 }, { from: 6012, to: 6020 }], [6011])).toBe(6021);

    // the floor, and wrapping from the ceiling back to it
    expect(nextFreePort(80, [], [])).toBe(1024);
    expect(nextFreePort(0, [], [])).toBe(1024);
    expect(nextFreePort(65535, [], [])).toBe(65535);
    expect(nextFreePort(65535, [], [65535])).toBe(1024);
    expect(nextFreePort(65536, [], [])).toBe(1024);
    expect(nextFreePort(65534, [{ from: 65530, to: 65535 }, { from: 1024, to: 1030 }], [1031])).toBe(1032);
  });

  it('takes the taken ports as numbers or as the entries the server lists', () => {
    expect(nextFreePort(6001, [], [6001, 6002])).toBe(6003);
    expect(nextFreePort(6001, [], [{ port: 6001 }, { port: 6002 }])).toBe(6003);
    expect(nextFreePort(6001, [], ports.taken.filter((t) => t.protocol === 'tcp'))).toBe(6003);
    expect(nextFreePort(6001, [], [6001, { port: 6002 }])).toBe(6003);
  });

  it('gives up when every port is spoken for', () => {
    expect(nextFreePort(2000, [{ from: 1024, to: 65535 }], [])).toBeNull();
    expect(nextFreePort(2000, [{ from: 1024, to: 3000 }, { from: 3002, to: 65535 }], [3001])).toBeNull();
    expect(nextFreePort(2000, [{ from: 1024, to: 3000 }, { from: 3002, to: 65535 }], [])).toBe(3001);
  });

  it('keeps to the limits it is given', () => {
    expect(nextFreePort(10, [], [], { min: 100, max: 102 })).toBe(100);
    expect(nextFreePort(102, [], [102], { min: 100, max: 102 })).toBe(100);
    expect(nextFreePort(101, [{ from: 100, to: 102 }], [], { min: 100, max: 102 })).toBeNull();
  });
});
