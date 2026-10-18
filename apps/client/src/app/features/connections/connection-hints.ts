/**
 * What the connection form says, while a connection is edited, about a listener: the port it picks (free, taken,
 * one of Sentinel's own) and the senders it lets in. The server checks all of it again when the connection is
 * saved; these are the same checks made early, with a free port to take instead of a refusal.
 */
import { FieldHint } from '../../shared/components/schema-form/schema-form.component';
import { ListenPort, ListenPorts, PortRange } from '../../shared/models/feed.model';
import { ListenInfo, joinHostPort, listenOf } from './listen-config';

/** Where a suggestion starts when the server offers no free port of its own. */
export const PORT_BASE = 6001;

/** The ports a listener may bind, until the server says otherwise. */
const PORT_MIN = 1024;
const PORT_MAX = 65535;

export interface PortBounds {
  min: number;
  max: number;
}

export interface ConnectionHintsInput {
  driverId: string | undefined;
  /** The config as it is being edited. */
  config: Record<string, unknown>;
  /** What the server reports about ports; none yet, or none at all, and there are no port hints. */
  ports?: ListenPorts | null;
  /** The connection being edited: the port it holds is its own, not a clash. */
  selfId?: string;
}

/**
 * The hints for the fields of a connection that listens, and none for one that does not. A hint is addressed
 * to the pointer the server reports problems at, so it lands on the field it is about.
 */
export function connectionHints(input: ConnectionHintsInput): FieldHint[] {
  const listen = listenOf(input.driverId, input.config);
  if (!listen) {
    return [];
  }
  const hints: FieldHint[] = [];
  const port = input.ports ? portHint(listen, input.ports, input.driverId === 'stream', input.selfId) : null;
  if (port) {
    hints.push(port);
  }
  const allow = allowlistHint(listen);
  if (allow) {
    hints.push(allow);
  }
  return hints;
}

/**
 * The first free port at or after `from`, going round from the top of the bounds back to the bottom, or null
 * when every port is spoken for. A port is spoken for when it lies in one of the `blocked` ranges (the server's
 * `reserved`) or is `taken`, given as port numbers or as the entries of the server's `taken`.
 */
export function nextFreePort(
  from: number,
  blocked: readonly PortRange[],
  taken: ReadonlyArray<number | Pick<ListenPort, 'port'>>,
  bounds: PortBounds = { min: PORT_MIN, max: PORT_MAX },
): number | null {
  const held = new Set(taken.map((t) => (typeof t === 'number' ? t : t.port)));
  const span = bounds.max - bounds.min + 1;
  const start = from >= bounds.min && from <= bounds.max ? from : bounds.min;
  for (let step = 0; step < span; step++) {
    const port = bounds.min + ((start - bounds.min + step) % span);
    if (!held.has(port) && !inRanges(port, blocked)) {
      return port;
    }
  }
  return null;
}

function inRanges(port: number, ranges: readonly PortRange[]): boolean {
  return ranges.some((range) => port >= range.from && port <= range.to);
}

/** What the server reports, read for one listener: the limits, Sentinel's ranges, and the other connections' ports. */
interface PortView {
  bounds: PortBounds;
  reserved: readonly PortRange[];
  held: readonly ListenPort[];
}

function viewOf(ports: ListenPorts, protocol: ListenInfo['protocol'], selfId: string | undefined): PortView {
  const taken = Array.isArray(ports.taken) ? ports.taken : [];
  return {
    bounds: {
      min: Number.isInteger(ports.min) ? ports.min : PORT_MIN,
      max: Number.isInteger(ports.max) ? ports.max : PORT_MAX,
    },
    reserved: Array.isArray(ports.reserved) ? ports.reserved : [],
    held: taken.filter((t) => t.protocol === protocol && t.feedId !== selfId),
  };
}

function isFree(view: PortView, port: number): boolean {
  return (
    Number.isInteger(port) &&
    port >= view.bounds.min &&
    port <= view.bounds.max &&
    !inRanges(port, view.reserved) &&
    !view.held.some((t) => t.port === port)
  );
}

function freeFrom(view: PortView, from: number): number | null {
  return nextFreePort(from, view.reserved, view.held, view.bounds);
}

/**
 * The port to offer when none is set: the server's suggestion if it is free here too. A suggestion that is a port
 * but is not free here (the server's view differs) leads to the next free one after it; anything that could not be
 * a port leads to the first free one from the base.
 */
function suggestion(view: PortView, suggested: unknown): number | null {
  const { min, max } = view.bounds;
  if (typeof suggested === 'number' && Number.isInteger(suggested) && suggested >= min && suggested <= max) {
    return isFree(view, suggested) ? suggested : freeFrom(view, suggested + 1);
  }
  return freeFrom(view, PORT_BASE);
}

/**
 * What to say about the port, in the order the server checks it: out of bounds, reserved, taken. A port that is
 * free gets no remark, and neither does one the field's own schema already refuses (tcp and udp keep the port in a
 * number field with a minimum and a maximum; a stream keeps it in an address, which is free text).
 */
function portHint(
  listen: ListenInfo,
  ports: ListenPorts,
  inAddress: boolean,
  selfId: string | undefined,
): FieldHint | null {
  const view = viewOf(ports, listen.protocol, selfId);
  const path = listen.portPath;
  const port = listen.port;

  /** "Use 6003": the port, or in a stream the address with the port put in. */
  const use = (free: number | null): FieldHint['action'] =>
    free === null ? undefined : { label: `Use ${free}`, value: inAddress ? joinHostPort(listen.host, free) : free };

  if (port === null) {
    const free = suggestion(view, ports.suggested?.[listen.protocol]);
    if (free === null) {
      return null;
    }
    const protocol = listen.protocol.toUpperCase();
    return {
      path,
      level: 'info',
      message: `Port ${free} is not used by Sentinel or another ${protocol} connection.`,
      action: use(free),
    };
  }

  if (port < view.bounds.min || port > view.bounds.max) {
    if (!inAddress) {
      return null;
    }
    const { min, max } = view.bounds;
    const message = port < min ? `Listening ports start at ${min}.` : `Listening ports end at ${max}.`;
    return { path, level: 'warn', message };
  }

  if (inRanges(port, view.reserved)) {
    const message = `Port ${port} is one of Sentinel's own (INGEST_RESERVED_PORTS).`;
    return { path, level: 'warn', message, action: use(freeFrom(view, port + 1)) };
  }

  const holder = view.held.find((t) => t.port === port);
  if (holder) {
    return { path, level: 'warn', message: takenMessage(holder), action: use(freeFrom(view, port + 1)) };
  }
  return null;
}

function takenMessage(holder: ListenPort): string {
  // a connection the server sent no name for is known by its id
  const named = typeof holder.feedName === 'string' && holder.feedName.trim() !== '';
  const name = named ? `${holder.feedName} (${holder.feedId})` : holder.feedId;
  const message = `Port ${holder.port}/${holder.protocol} is already used by ${name}.`;
  return holder.enabled === false ? `${message} Disabled connections keep their port.` : message;
}

/**
 * An allowlist left empty lets in anyone who can reach the port; for UDP, whoever can forge an address too. Under
 * mutual TLS the certificate is the gate, so an open list is information, not a risk.
 */
function allowlistHint(listen: ListenInfo): FieldHint | null {
  if (listen.allow.length > 0) {
    return null;
  }
  if (listen.mutualTls) {
    const message =
      'Empty: any address can attempt a connection, ' +
      'but only senders with a certificate signed by the client CA get in.';
    return { path: listen.allowPath, level: 'info', message };
  }
  const where = listen.port === null ? 'the port' : `port ${listen.port}`;
  let message = `Empty: anyone who can reach ${where} can connect. List the senders' IP addresses or CIDR ranges.`;
  if (listen.protocol === 'udp') {
    message += ' UDP source addresses can be forged; on an untrusted network prefer TCP with mutual TLS.';
  }
  return { path: listen.allowPath, level: 'warn', message };
}
