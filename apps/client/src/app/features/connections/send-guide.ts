import { Feed } from '../../shared/models/feed.model';

/** Where an external system sends to, or where Sentinel connects to. */
export interface SendTarget {
  mode: 'listen' | 'connect';
  protocol: 'tcp' | 'udp';
  host: string;
  port: number;
  format: string;
  encoding: 'protobuf' | 'json';
  tls: boolean;
  requireClientCert: boolean;
}

/** The proto files a sender needs, served from assets/proto/sentinel/v1/. */
export const PROTO_FILES = ['observation.proto', 'common.proto', 'entity.proto', 'platform.proto'];

function recordOf(v: unknown): Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

/** A TCP or UDP connection's target, from its running health (host) and its config; null for other drivers. */
export function sendTarget(feed: Feed): SendTarget | null {
  if (feed.driver !== 'tcp' && feed.driver !== 'udp') return null;
  const config = recordOf(feed.config);
  const detail = recordOf(feed.status?.detail);
  const tls = recordOf(config['tls']);
  const mode: 'listen' | 'connect' = feed.driver === 'tcp' && config['mode'] === 'connect' ? 'connect' : 'listen';
  const running = recordOf(mode === 'listen' ? detail['listen'] : detail['connect']);
  return {
    mode,
    protocol: feed.driver,
    host: mode === 'connect' ? String(config['host'] ?? '') : String(running['host'] ?? ''),
    port: Number(running['port'] ?? config['port'] ?? 0),
    format: String(config['format'] ?? 'sentinel'),
    encoding: config['encoding'] === 'json' ? 'json' : 'protobuf',
    tls: !!tls['enabled'],
    requireClientCert: !!tls['requireClientCert'],
  };
}

/** "host:port/protocol"; the host is "listener" until the connection runs. */
export function endpointLabel(t: SendTarget): string {
  return `${t.host || 'listener'}:${t.port}/${t.protocol}`;
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

/** A one-line test with netcat, for plain-text JSON only. */
export function netcatCommand(t: SendTarget): string | null {
  if (t.format !== 'sentinel' || t.encoding !== 'json' || t.tls || !t.host) return null;
  return `echo '${jsonExample()}' | nc ${t.protocol === 'udp' ? '-u ' : ''}-w1 ${t.host} ${t.port}`;
}

/** A Go sender for binary protobuf. */
export function goSnippet(t: SendTarget): string {
  const addr = `${t.host || 'LISTENER_HOST'}:${t.port}`;
  const body = t.protocol === 'tcp'
    ? `conn, err := net.Dial("tcp", "${addr}")
if err != nil { log.Fatal(err) }
defer conn.Close()
// One length-delimited message per observation (protodelim).
if _, err := protodelim.MarshalTo(conn, obs); err != nil { log.Fatal(err) }`
    : `conn, err := net.Dial("udp", "${addr}")
if err != nil { log.Fatal(err) }
defer conn.Close()
// One datagram per observation.
data, _ := proto.Marshal(obs)
conn.Write(data)`;
  return `// go get google.golang.org/protobuf, then generate Go code from the .proto files
obs := &sentinelv1.Observation{
  SourceEntityId: "example-1",
  EntityType:     sentinelv1.EntityType_ENTITY_TYPE_VEHICLE,
  Position:       &sentinelv1.Coordinate{Latitude: 51.5, Longitude: -0.12},
}
${body}`;
}

/** A Python sender for binary protobuf. */
export function pythonSnippet(t: SendTarget): string {
  const host = t.host || 'LISTENER_HOST';
  const send = t.protocol === 'tcp'
    ? `from google.protobuf.internal.encoder import _VarintBytes
with socket.create_connection(("${host}", ${t.port})) as s:
    data = obs.SerializeToString()
    s.sendall(_VarintBytes(len(data)) + data)  # length-delimited`
    : `s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.sendto(obs.SerializeToString(), ("${host}", ${t.port}))  # one datagram per observation`;
  return `# Save the downloaded .proto files in a directory named sentinel/v1/ first:
# they import each other by that path ("sentinel/v1/common.proto"), so protoc
# must be run from the directory above it.
# pip install protobuf; protoc --python_out=. sentinel/v1/*.proto
import socket
from sentinel.v1 import observation_pb2

obs = observation_pb2.Observation(source_entity_id="example-1")
obs.position.latitude, obs.position.longitude = 51.5, -0.12
${send}`;
}
