/**
 * Where a connection listens, read from its config the way the server's `driver.ListenOf` reads
 * it, so the client and the server agree on what "listens" means.
 */

export interface ListenInfo {
  protocol: 'tcp' | 'udp';
  /** The port it binds; null while the config has none the server could read. */
  port: number | null;
  /** The interface it binds; empty for all of them. */
  host: string;
  /** Where the server reports problems with the port and with the allowlist (JSON pointers). */
  portPath: string;
  allowPath: string;
  /** IP addresses and CIDR ranges allowed to send; empty is open to anyone who can reach the port. */
  allow: string[];
}

function recordOf(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

function stringsOf(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((entry): entry is string => typeof entry === 'string') : [];
}

/**
 * Whether a feed of this driver and config listens on a port, and where. The tcp driver listens in
 * listen mode only, udp always does, and stream when its transport is a tcpserver or udp.
 */
export function listenOf(driverId: string | undefined, config: Record<string, unknown>): ListenInfo | null {
  const c = recordOf(config);
  switch (driverId) {
    case 'tcp':
      return c['mode'] === 'listen' ? flatListen('tcp', c) : null;
    case 'udp':
      return flatListen('udp', c);
    case 'stream': {
      const transport = recordOf(c['transport']);
      const kind = transport['kind'];
      if (kind !== 'tcpserver' && kind !== 'udp') {
        return null;
      }
      const address = splitHostPort(typeof transport['address'] === 'string' ? transport['address'] : '');
      return {
        protocol: kind === 'tcpserver' ? 'tcp' : 'udp',
        port: address.port,
        host: address.host,
        portPath: '/config/transport/address',
        allowPath: '/config/transport/allow',
        allow: stringsOf(transport['allow']),
      };
    }
    default:
      return null;
  }
}

/** The tcp and udp drivers keep the port, the bind address and the allowlist at the top of their config. */
function flatListen(protocol: 'tcp' | 'udp', c: Record<string, unknown>): ListenInfo {
  const port = c['port'];
  return {
    protocol,
    port: typeof port === 'number' && Number.isInteger(port) ? port : null,
    host: typeof c['address'] === 'string' ? c['address'] : '',
    portPath: '/config/port',
    allowPath: '/config/allow',
    allow: stringsOf(c['allow']),
  };
}

/**
 * Splits "host:port", ":port" and "[ipv6]:port" like Go's net.SplitHostPort. An address without a
 * numeric port keeps what it has as the host and has no port.
 */
function splitHostPort(address: string): { host: string; port: number | null } {
  let host = address;
  let rest = '';
  if (address.startsWith('[')) {
    const end = address.indexOf(']');
    if (end > 0) {
      host = address.slice(1, end);
      rest = address.slice(end + 1);
    }
  } else {
    // Exactly one colon splits; none means no port, and several are a bare IPv6 address.
    const colon = address.indexOf(':');
    if (colon >= 0 && colon === address.lastIndexOf(':')) {
      host = address.slice(0, colon);
      rest = address.slice(colon);
    }
  }
  const port = /^:(\d+)$/.exec(rest);
  return { host, port: port ? Number(port[1]) : null };
}

/** "host:port", with an IPv6 host in brackets like Go's net.JoinHostPort. */
export function joinHostPort(host: string, port: number): string {
  return host.includes(':') ? `[${host}]:${port}` : `${host}:${port}`;
}
