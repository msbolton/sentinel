import { Feed } from '../../shared/models/feed.model';
import { joinHostPort } from './listen-config';

/** Where an external system sends to, or where Sentinel connects to. */
export interface SendTarget {
  mode: 'listen' | 'connect';
  protocol: 'tcp' | 'udp';
  host: string;
  port: number;
  format: string;
  encoding: 'protobuf' | 'json';
  /** How the stream is cut into messages, as the connection runs it: a key of FRAMING_NOTES. */
  framing: string;
  tls: boolean;
  requireClientCert: boolean;
  /** IP addresses and CIDR ranges allowed to send; empty is open to anyone who can reach the port. */
  allow: string[];
}

/** The proto files a sender needs, served from assets/proto/sentinel/v1/. */
export const PROTO_FILES = ['observation.proto', 'common.proto', 'entity.proto', 'platform.proto'];

/** What the examples say until the listener's host is known. */
const LISTENER_HOST = 'LISTENER_HOST';

/** Where the examples assume the generated Go code lives. */
const SENTINEL_GO_IMPORT = 'example.com/yourapp/sentinel/v1';

function recordOf(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

function stringsOf(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((entry): entry is string => typeof entry === 'string') : [];
}

function textOf(v: unknown): string {
  return typeof v === 'string' ? v : '';
}

/** A TCP or UDP connection's target, from its running health (host) and its config; null for other drivers. */
export function sendTarget(feed: Feed): SendTarget | null {
  if (feed.driver !== 'tcp' && feed.driver !== 'udp') return null;
  const protocol = feed.driver;
  const config = recordOf(feed.config);
  const detail = recordOf(feed.status?.detail);
  const tls = recordOf(config['tls']);
  const mode: 'listen' | 'connect' = protocol === 'tcp' && config['mode'] === 'connect' ? 'connect' : 'listen';
  const running = recordOf(mode === 'listen' ? detail['listen'] : detail['connect']);
  const format = textOf(config['format']) || 'sentinel';
  const encoding = config['encoding'] === 'json' ? 'json' : 'protobuf';
  // Datagrams have no TLS, whatever a stray key says; a client certificate needs TLS.
  const tlsOn = protocol === 'tcp' && !!tls['enabled'];
  // A running connection reports the framing it uses; before that, the config's override (TCP only).
  const override = protocol === 'tcp' ? textOf(config['framing']) : '';
  const framing = textOf(detail['framing']) || override || impliedFraming(protocol, format, encoding);
  return {
    mode,
    protocol,
    host: mode === 'connect' ? String(config['host'] ?? '') : String(running['host'] ?? ''),
    port: Number(running['port'] ?? config['port'] ?? 0),
    format,
    encoding,
    framing,
    tls: tlsOn,
    requireClientCert: tlsOn && !!tls['requireClientCert'],
    allow: stringsOf(config['allow']),
  };
}

/**
 * The framing a format and encoding imply when the config does not override it: the server's
 * tcp.FramingFor and udp.framingFor. The encoding matters to the sentinel format only.
 */
export function impliedFraming(protocol: 'tcp' | 'udp', format: string, encoding: 'protobuf' | 'json'): string {
  const sentinel = format === '' || format === 'sentinel';
  if (protocol === 'udp') {
    if (sentinel) return encoding === 'json' ? 'line' : 'message';
    return format === 'cot' || format === 'link16' ? 'message' : 'line';
  }
  if (sentinel) return encoding === 'json' ? 'line' : 'delimited';
  if (format === 'cot') return 'xmlevent';
  if (format === 'beast') return 'beast';
  return 'line';
}

/** What each framing looks like on the wire (the server's transport/framing package). */
export const FRAMING_NOTES: Record<string, string> = {
  line: 'one message per line, ended by a newline',
  lengthprefix: 'each message preceded by its length as 2 bytes, big-endian',
  delimited: 'each message preceded by its length as a protobuf varint',
  beast: 'Mode-S Beast binary frames',
  xmlevent: 'one <event>…</event> element per message (Cursor-on-Target)',
  message: 'one message per datagram',
};

/** "host:port/protocol"; the host is "listener" until the connection runs. */
export function endpointLabel(t: SendTarget): string {
  return `${joinHostPort(t.host || 'listener', t.port)}/${t.protocol}`;
}

/** The host an example connects to: the listener's, or a placeholder until it is known. */
function hostOf(t: SendTarget): string {
  return t.host || LISTENER_HOST;
}

/**
 * A value as one shell word: bare when it is plainly safe, else in single quotes with each ' written
 * as '\''. Brackets are not plainly safe: to a shell they are a pattern, and zsh refuses one that
 * matches nothing, so an IPv6 endpoint is quoted.
 */
export function shellQuote(value: string): string {
  return /^[A-Za-z0-9_.:-]+$/.test(value) ? value : `'${value.replace(/'/g, `'\\''`)}'`;
}

/**
 * A double-quoted string literal for Go or Python source, which spell these escapes alike. Printable
 * ASCII is kept as it is. The quote and the backslash get a backslash, and newline, carriage return
 * and tab are written \n, \r and \t. Every other character, the other control characters and DEL
 * as well as all non-ASCII text (printable or not), is written as \xNN, \uNNNN or \UNNNNNNNN, so a
 * value can neither end the literal nor start new code. A lone surrogate is not text, so it
 * becomes U+FFFD.
 */
function stringLiteral(value: string): string {
  let out = '"';
  for (const ch of value) {
    const code = ch.codePointAt(0)!;
    if (ch === '"' || ch === '\\') out += `\\${ch}`;
    else if (ch === '\n') out += '\\n';
    else if (ch === '\r') out += '\\r';
    else if (ch === '\t') out += '\\t';
    else if (code >= 0x20 && code < 0x7f) out += ch;
    else out += escapeCode(code);
  }
  return `${out}"`;
}

function escapeCode(code: number): string {
  const text = code >= 0xd800 && code <= 0xdfff ? 0xfffd : code;
  const hex = (digits: number) => text.toString(16).padStart(digits, '0');
  if (text < 0x80) return `\\x${hex(2)}`;
  return text <= 0xffff ? `\\u${hex(4)}` : `\\U${hex(8)}`;
}

/** A minimal Observation in protobuf JSON form. */
export function jsonExample(): string {
  return JSON.stringify({
    sourceEntityId: 'example-1',
    entityType: 'ENTITY_TYPE_VEHICLE',
    displayName: 'Example 1',
    position: { latitude: 51.5, longitude: -0.12 },
  });
}

/** Up to this many observations fit in one JSON array line (the server's sentinelobs maxBatch). */
export const JSON_BATCH_MAX = 100;

/** Two observations as the JSON array one line may hold, in place of a single object. */
export function jsonBatchExample(): string {
  const first = JSON.parse(jsonExample()) as Record<string, unknown>;
  return JSON.stringify([
    first,
    {
      sourceEntityId: 'example-2',
      entityType: 'ENTITY_TYPE_VEHICLE',
      displayName: 'Example 2',
      position: { latitude: 51.51, longitude: -0.13 },
    },
  ]);
}

/**
 * Whether a sender writes Sentinel's own messages as JSON, one object per line (or, to batch, one
 * array of up to JSON_BATCH_MAX observations per line).
 */
function sendsJsonLines(t: SendTarget): boolean {
  return t.mode === 'listen' && t.format === 'sentinel' && t.encoding === 'json' && t.framing === 'line';
}

/**
 * Whether the Go and Python examples fit the connection: Sentinel's protobuf, written with a varint
 * length in front (TCP) or as one whole datagram (UDP). Any other framing needs different code.
 */
export function protobufExamplesFit(t: SendTarget): boolean {
  return t.format === 'sentinel' && t.encoding === 'protobuf' && t.framing === (t.protocol === 'tcp' ? 'delimited' : 'message');
}

/** Why there are no Go or Python examples for this connection; empty when there are. */
export function examplesNote(t: SendTarget): string {
  if (protobufExamplesFit(t)) return '';
  if (t.format !== 'sentinel') {
    return `This connection reads ${t.format} messages from a device, not Sentinel's own, so there are no Sentinel samples or examples to show. Point the device at ${endpointLabel(t)}.`;
  }
  if (t.encoding === 'json') {
    return 'The Go and Python examples write binary protobuf. This connection takes JSON, as in the sample above.';
  }
  const wants = t.protocol === 'tcp' ? 'length-delimited protobuf' : 'one protobuf message per datagram';
  return `The Go and Python examples write ${wants}, but this connection uses ${t.framing} framing (${FRAMING_NOTES[t.framing] ?? t.framing}), so they would not be accepted as written.`;
}

/** A one-line test with netcat, for plain-text JSON only. */
export function netcatCommand(t: SendTarget): string | null {
  if (!sendsJsonLines(t) || t.tls) return null;
  const host = hostOf(t);
  // A quote does not stop nc reading a host that begins with "-" as an option; "--" does.
  const where = `${host.startsWith('-') ? '-- ' : ''}${shellQuote(host)} ${shellQuote(String(t.port))}`;
  return `echo ${shellQuote(jsonExample())} | nc ${t.protocol === 'udp' ? '-u ' : ''}-w1 ${where}`;
}

/**
 * A one-line test with OpenSSL for JSON over TLS. -servername only says which name to ask for;
 * -verify_hostname makes openssl check the certificate against it. ca.pem is the CA that signed the listener's
 * certificate; a listener that requires client certificates also needs client.pem and client.key.
 */
export function tlsTestCommand(t: SendTarget): string | null {
  if (!t.tls || t.protocol !== 'tcp' || !sendsJsonLines(t)) return null;
  const host = hostOf(t);
  const client = t.requireClientCert ? ' -cert client.pem -key client.key' : '';
  return `echo ${shellQuote(jsonExample())} | openssl s_client -quiet -no_ign_eof -verify_return_error -connect ${shellQuote(joinHostPort(host, t.port))} -servername ${shellQuote(host)} -verify_hostname ${shellQuote(host)} -CAfile ca.pem${client}`;
}

/** Go source is written here with two-space indents and handed out with tabs, as gofmt lays it out. */
function gofmtIndent(src: string): string {
  return src.replace(/^(?: {2})+/gm, (spaces) => '\t'.repeat(spaces.length / 2));
}

/** The Go statements that open a TLS connection: trust the listener's CA, and present a certificate if it asks for one. */
function goTlsDial(t: SendTarget, host: string, addr: string): string {
  const clientCert = t.requireClientCert
    ? `// The listener requires a client certificate, signed by the CA it trusts.
cert, err := tls.LoadX509KeyPair("client.pem", "client.key")
if err != nil {
  log.Fatal(err)
}
cfg.Certificates = []tls.Certificate{cert}
`
    : '';
  return `ca, err := os.ReadFile("ca.pem") // the CA that signed the listener's certificate
if err != nil {
  log.Fatal(err)
}
roots := x509.NewCertPool()
if !roots.AppendCertsFromPEM(ca) {
  log.Fatal("no certificates in ca.pem")
}
cfg := &tls.Config{RootCAs: roots, ServerName: ${stringLiteral(host)}}
${clientCert}conn, err := tls.Dial("tcp", ${stringLiteral(addr)}, cfg)`;
}

/** A complete Go sender for binary protobuf: its imports are the ones this variant uses. */
export function goSnippet(t: SendTarget): string {
  const host = hostOf(t);
  const addr = joinHostPort(host, t.port);
  const tls = t.protocol === 'tcp' && t.tls;
  const imports = [
    tls ? ['crypto/tls', 'crypto/x509', 'log', 'os'] : ['log', 'net'],
    [t.protocol === 'tcp' ? 'google.golang.org/protobuf/encoding/protodelim' : 'google.golang.org/protobuf/proto'],
    [`sentinelv1 "${SENTINEL_GO_IMPORT}"`],
  ]
    .map((group) => group.map((spec) => `  ${spec.includes(' ') ? spec : `"${spec}"`}`).join('\n'))
    .join('\n\n');

  const observation = `obs := &sentinelv1.Observation{
  SourceEntityId: "example-1",
  EntityType:     sentinelv1.EntityType_ENTITY_TYPE_VEHICLE,
  Position:       &sentinelv1.Coordinate{Latitude: 51.5, Longitude: -0.12},
}`;

  let send: string;
  if (t.protocol === 'udp') {
    send = `data, err := proto.Marshal(obs)
if err != nil {
  log.Fatal(err)
}

conn, err := net.Dial("udp", ${stringLiteral(addr)})
if err != nil {
  log.Fatal(err)
}
defer conn.Close()

// One datagram per observation.
if _, err := conn.Write(data); err != nil {
  log.Fatal(err)
}`;
  } else {
    send = `${tls ? goTlsDial(t, host, addr) : `conn, err := net.Dial("tcp", ${stringLiteral(addr)})`}
if err != nil {
  log.Fatal(err)
}
defer conn.Close()

// One length-delimited message per observation.
if _, err := protodelim.MarshalTo(conn, obs); err != nil {
  log.Fatal(err)
}`;
  }

  const body = `${observation}\n\n${send}`
    .split('\n')
    .map((line) => (line ? `  ${line}` : line))
    .join('\n');
  return gofmtIndent(`// Save the downloaded .proto files in a directory named sentinel/v1/ (they import each
// other by that path). From the directory above it, generate the Go code:
//
//   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
//   protoc --go_out=. --go_opt=paths=source_relative sentinel/v1/*.proto
//
// Then, in your module (the import below assumes it is example.com/yourapp), go get
// google.golang.org/protobuf and import the generated package:
//
//   import sentinelv1 "${SENTINEL_GO_IMPORT}"

package main

import (
${imports}
)

func main() {
${body}
}
`);
}

/** A Python sender for binary protobuf. */
export function pythonSnippet(t: SendTarget): string {
  const host = hostOf(t);
  const tcp = t.protocol === 'tcp';
  const tls = tcp && t.tls;
  const header = `# Save the downloaded .proto files in a directory named sentinel/v1/ first:
# they import each other by that path ("sentinel/v1/common.proto"), so protoc
# must be run from the directory above it.
# pip install -U protobuf (a runtime older than protoc cannot load its output); protoc --python_out=. sentinel/v1/*.proto
import socket${tls ? '\nimport ssl\nimport time' : ''}
from sentinel.v1 import entity_pb2, observation_pb2`;
  const varint = `def varint(n):
    out = bytearray()
    while True:
        b, n = n & 0x7F, n >> 7
        out.append(b | 0x80 if n else b)
        if not n:
            return bytes(out)`;
  const message = `obs = observation_pb2.Observation(
    source_entity_id="example-1",
    entity_type=entity_pb2.ENTITY_TYPE_VEHICLE,
)
obs.position.latitude, obs.position.longitude = 51.5, -0.12
data = obs.SerializeToString()`;

  let send: string;
  if (!tcp) {
    send = `# The first address the name resolves to, IPv4 or IPv6.
family, socktype, proto, _, addr = socket.getaddrinfo(${stringLiteral(host)}, ${t.port}, type=socket.SOCK_DGRAM)[0]
with socket.socket(family, socktype, proto) as s:
    s.sendto(data, addr)  # one datagram per observation`;
  } else if (tls) {
    const clientCert = t.requireClientCert ? `ctx.load_cert_chain("client.pem", "client.key")  # a certificate the listener's client CA signed\n` : '';
    send = `ctx = ssl.create_default_context(cafile="ca.pem")  # the CA that signed the listener's certificate
${clientCert}with socket.create_connection((${stringLiteral(host)}, ${t.port})) as raw:
    with ctx.wrap_socket(raw, server_hostname=${stringLiteral(host)}) as s:
        s.sendall(varint(len(data)) + data)  # length-delimited
        # Closing at once can reset the connection and lose the message, because TLS 1.3 session
        # tickets are still unread. A sender that stays connected does not need to wait.
        time.sleep(0.5)`;
  } else {
    send = `with socket.create_connection((${stringLiteral(host)}, ${t.port})) as s:
    s.sendall(varint(len(data)) + data)  # length-delimited`;
  }
  // PEP 8: two blank lines around a function, one between statements.
  const top = tcp ? `${header}\n\n\n${varint}\n\n\n${message}` : `${header}\n\n${message}`;
  return `${top}\n\n${send}\n`;
}

/** A counter the listener or the dialling connection reports in `status.detail`, and what it counts. */
export interface CounterDef {
  key: string;
  label: string;
  /** What the number counts, in words for the person reading it. */
  help: string;
  /** Whether this connection can ever raise it, so a counter that is always zero stays hidden. */
  applies: (t: SendTarget) => boolean;
}

const listens = (t: SendTarget) => t.mode === 'listen';
const listensOnTcp = (t: SendTarget) => listens(t) && t.protocol === 'tcp';

/** The counters, in the order they are shown; semantics from the tcpserver, udp and tcpclient transports. */
export const COUNTERS: CounterDef[] = [
  {
    key: 'accepted',
    label: 'Accepted',
    help: 'Messages that passed the allowlist and the rate limits and were handed on for decoding.',
    applies: listens,
  },
  {
    key: 'rejected',
    label: 'Not allowed',
    help: 'Senders turned away because their address is not on the allowlist: TCP connections closed before any data was read, UDP datagrams dropped.',
    applies: listens,
  },
  {
    key: 'refused',
    label: 'Over connection limit',
    help: 'TCP connections closed at once because the connection limit (maxConnections) was already reached.',
    applies: listensOnTcp,
  },
  {
    key: 'rateLimited',
    label: 'Rate limited',
    help: 'Messages dropped because all senders together went over the messages-per-second limit (maxMessagesPerSec).',
    applies: listens,
  },
  {
    key: 'senderRateLimited',
    label: 'Rate limited (per sender)',
    help: 'Messages dropped because one sender went over its own messages-per-second limit (maxMessagesPerSecPerSender).',
    applies: listens,
  },
  {
    key: 'oversize',
    label: 'Too large',
    help: 'Messages bigger than the size limit (maxMessageBytes). A UDP datagram is dropped; a TCP connection is closed.',
    applies: listens,
  },
  {
    key: 'kernelDropped',
    label: 'Dropped by the OS',
    help: 'Datagrams the operating system dropped on this socket before Sentinel read them, usually because the receive buffer was full (Linux only).',
    applies: (t) => listens(t) && t.protocol === 'udp',
  },
  {
    key: 'tlsFailed',
    label: 'TLS failed',
    help: 'TCP connections whose TLS handshake failed, and TLS clients that reached a listener without TLS.',
    applies: listensOnTcp,
  },
  {
    key: 'tlsReloads',
    label: 'TLS reloads',
    help: 'TLS material reloads that succeeded.',
    applies: (t) => listensOnTcp(t) && t.tls,
  },
  {
    key: 'tlsReloadFailed',
    label: 'TLS reload failed',
    help: 'Times a changed certificate or key could not be loaded. The previous certificate stays in use.',
    applies: (t) => listensOnTcp(t) && t.tls,
  },
  {
    key: 'malformed',
    label: 'Undecodable',
    help: 'Messages that arrived but could not be decoded, usually a wrong format or framing. Samples go to the dead-letter topic.',
    applies: () => true,
  },
  {
    key: 'messages',
    label: 'Messages',
    help: 'Messages read from the remote system.',
    applies: (t) => t.mode === 'connect',
  },
  {
    key: 'reconnects',
    label: 'Reconnects',
    help: 'Times Sentinel connected again after the connection dropped.',
    applies: (t) => t.mode === 'connect',
  },
];

/**
 * A sender's timestamp, or null while it has none. Go writes "never" as the zero time (year 1),
 * and the server leaves the field out altogether in newer versions.
 */
export function senderTime(iso: string | null | undefined): string | null {
  if (!iso) return null;
  const ms = Date.parse(iso);
  return Number.isNaN(ms) || ms <= 0 ? null : iso;
}
