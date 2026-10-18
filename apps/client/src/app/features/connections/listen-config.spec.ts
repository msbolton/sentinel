import { joinHostPort, listenOf } from './listen-config';

describe('listenOf', () => {
  it('tcp listens only in listen mode', () => {
    expect(listenOf('tcp', { mode: 'listen', port: 6001, address: '10.0.0.1', allow: ['10.0.0.0/8', '192.168.1.5'] })).toEqual({
      protocol: 'tcp',
      port: 6001,
      host: '10.0.0.1',
      portPath: '/config/port',
      allowPath: '/config/allow',
      allow: ['10.0.0.0/8', '192.168.1.5'],
    });
    expect(listenOf('tcp', { mode: 'connect', host: 'feeds.example', port: 7000 })).toBeNull();
    // Like the server, a config with no mode at all does not listen.
    expect(listenOf('tcp', { port: 6001 })).toBeNull();
  });

  it('udp always listens', () => {
    expect(listenOf('udp', { port: 10110 })).toEqual({
      protocol: 'udp',
      port: 10110,
      host: '',
      portPath: '/config/port',
      allowPath: '/config/allow',
      allow: [],
    });
    // Even before a port is chosen.
    expect(listenOf('udp', {})).toEqual(expect.objectContaining({ protocol: 'udp', port: null }));
  });

  it('stream listens for tcpserver and udp, reading the port from the address (IPv6 too)', () => {
    const stream = (transport: Record<string, unknown>) => listenOf('stream', { transport, decoder: { name: 'json' } });
    const paths = { portPath: '/config/transport/address', allowPath: '/config/transport/allow' };

    expect(stream({ kind: 'tcpserver', address: ':4001', allow: ['10.0.0.0/8'] })).toEqual({
      protocol: 'tcp',
      port: 4001,
      host: '',
      allow: ['10.0.0.0/8'],
      ...paths,
    });
    expect(stream({ kind: 'udp', address: '0.0.0.0:10110' })).toEqual({ protocol: 'udp', port: 10110, host: '0.0.0.0', allow: [], ...paths });
    expect(stream({ kind: 'tcpserver', address: '[::]:4001' })).toEqual(expect.objectContaining({ protocol: 'tcp', port: 4001, host: '::' }));
    expect(stream({ kind: 'udp', address: '[2001:db8::1]:5000' })).toEqual(expect.objectContaining({ port: 5000, host: '2001:db8::1' }));
  });

  it('other drivers never listen', () => {
    expect(listenOf('opensky', { mode: 'listen', port: 6001 })).toBeNull();
    expect(listenOf('tak', { address: 'tak.example.org:8089' })).toBeNull();
    expect(listenOf(undefined, { port: 6001 })).toBeNull();
    // A stream transport that dials out, or talks to a broker, binds nothing.
    for (const kind of ['tcpclient', 'mqtt', 'stomp', 'websocket', '']) {
      expect(listenOf('stream', { transport: { kind, address: ':4001' } })).toBeNull();
    }
    expect(listenOf('stream', {})).toBeNull();
  });

  it('a missing port is null', () => {
    expect(listenOf('tcp', { mode: 'listen' })!.port).toBeNull();
    // The server reads only a whole JSON number as the port.
    expect(listenOf('tcp', { mode: 'listen', port: '6001' })!.port).toBeNull();
    expect(listenOf('udp', { port: 6001.5 })!.port).toBeNull();
    for (const address of [undefined, '', 'localhost', '[::1]', '::1', ':http', 'host:', ':-1']) {
      const info = listenOf('stream', { transport: { kind: 'tcpserver', address } });
      expect(info).not.toBeNull();
      expect(info!.port).toBeNull();
    }
  });

  it('ignores a malformed allowlist and non-string entries', () => {
    expect(listenOf('udp', { port: 1, allow: 'anyone' })!.allow).toEqual([]);
    expect(listenOf('udp', { port: 1, allow: ['10.0.0.1', 5, null] })!.allow).toEqual(['10.0.0.1']);
  });
});

describe('joinHostPort', () => {
  it('joins a host and a port, bracketing an IPv6 host', () => {
    expect(joinHostPort('10.0.0.1', 6001)).toBe('10.0.0.1:6001');
    expect(joinHostPort('sentinel.example', 443)).toBe('sentinel.example:443');
    expect(joinHostPort('', 6001)).toBe(':6001');
    expect(joinHostPort('::', 6001)).toBe('[::]:6001');
    expect(joinHostPort('2001:db8::1', 5000)).toBe('[2001:db8::1]:5000');
  });
});
