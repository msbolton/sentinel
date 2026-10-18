import { readFileSync } from 'fs';
import { join } from 'path';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { SchemaFormChange, SchemaFormComponent } from './schema-form.component';
import { JsonSchema, ListenPorts } from '../../models/feed.model';
import { connectionHints } from '../../../features/connections/connection-hints';
import { listenOf } from '../../../features/connections/listen-config';

/** The schema an ingest driver really ships, so an edit to it that the form cannot honour fails here. */
function schemaOf(driver: string): JsonSchema {
  const file = join(__dirname, '../../../../../../ingest-service/internal/source/driver', driver, 'schema.json');
  return JSON.parse(readFileSync(file, 'utf-8')) as JsonSchema;
}

describe('SchemaFormComponent with the driver schemas', () => {
  let fixture: ComponentFixture<SchemaFormComponent>;
  let component: SchemaFormComponent;
  let el: HTMLElement;
  let changes: SchemaFormChange[];

  function last(): SchemaFormChange {
    return changes[changes.length - 1];
  }

  function render(driver: string, value: Record<string, unknown> = {}, secretFields: string[] = []): void {
    fixture.componentRef.setInput('schema', schemaOf(driver));
    fixture.componentRef.setInput('secretFields', secretFields);
    fixture.componentRef.setInput('value', value);
    fixture.detectChanges();
  }

  /** The secrets a driver's schema describes, which the catalog hands the form as `secretFields`. */
  function secretsOf(driver: string): string[] {
    return Object.keys(schemaOf(driver)['x-secrets'] ?? {});
  }

  function setInput(selector: string, value: string): void {
    const input = el.querySelector<HTMLInputElement>(selector);
    if (!input) {
      throw new Error(`no input for ${selector}`);
    }
    input.value = value;
    input.dispatchEvent(new Event('input'));
    fixture.detectChanges();
  }

  /** What a JSON field shows, parsed. */
  function jsonIn(pointer: string): unknown {
    const textarea = el.querySelector<HTMLTextAreaElement>(`[data-pointer="${pointer}"] textarea`);
    if (!textarea) {
      throw new Error(`no JSON field at ${pointer}`);
    }
    return JSON.parse(textarea.value);
  }

  function shown(pointer: string): boolean {
    return el.querySelector(`[data-pointer="${pointer}"]`) !== null;
  }

  /** The client-side messages a field shows. */
  function messagesOf(pointer: string): Array<string | undefined> {
    return Array.from(el.querySelectorAll(`[data-pointer="${pointer}"] > .sf-error`)).map((n) => n.textContent?.trim());
  }

  /** The messages the server sent for a field, as shown. */
  function serverMessagesOf(pointer: string): Array<string | undefined> {
    return Array.from(el.querySelectorAll(`[data-pointer="${pointer}"] > .sf-error-server`)).map((n) => n.textContent?.trim());
  }

  /** Whether a field is marked as required. */
  function marked(pointer: string): boolean {
    return el.querySelector(`[data-pointer="${pointer}"] > label .sf-required`) !== null;
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({ imports: [SchemaFormComponent] }).compileComponents();
    fixture = TestBed.createComponent(SchemaFormComponent);
    component = fixture.componentInstance;
    el = fixture.nativeElement as HTMLElement;
    changes = [];
    component.changed.subscribe((c) => changes.push(c));
  });

  it('tcp: a new connection starts in listen mode', () => {
    render('tcp');

    expect(component.control('/config/mode')?.value).toBe('listen');
    expect(last().config['mode']).toBe('listen');
    expect(shown('/config/allow')).toBe(true);
    expect(shown('/config/host')).toBe(false);
  });

  it('tcp: listen mode needs a port of 1024 or more', () => {
    render('tcp');

    // a new connection listens, and a listener needs a port
    expect(el.querySelector('[data-pointer="/config/port"] > label .sf-required')).not.toBeNull();
    expect(last().valid).toBe(false);

    setInput('input[name="port"]', '80');
    expect(messagesOf('/config/port')).toEqual(['Must be at least 1024.']);
    expect(last().valid).toBe(false);

    setInput('input[name="port"]', '6001');
    expect(messagesOf('/config/port')).toEqual([]);
    expect(last().valid).toBe(true);

    // connecting needs a host and a port, and any port will do
    render('tcp', { mode: 'connect', port: 80 });
    expect(el.querySelector('[data-pointer="/config/host"] > label .sf-required')).not.toBeNull();
    expect(last().valid).toBe(false);

    setInput('input[name="host"]', 'radar.example');
    expect(messagesOf('/config/port')).toEqual([]);
    expect(last().valid).toBe(true);
  });

  it('tcp: keeps keys the schema does not describe, and drops the ones connect mode hides', () => {
    render('tcp', { mode: 'connect', host: 'h', port: 7000, allow: ['10.0.0.0/8'], legacyKey: 1 });

    expect(last().config).toMatchObject({ mode: 'connect', host: 'h', port: 7000, legacyKey: 1 });
    expect(last().config).not.toHaveProperty('allow');
  });

  it('tcp: drops a tls key the schema closes the group to, and names nothing', () => {
    // a connection stored before tls forbade file paths still carries one
    render('tcp', { mode: 'listen', port: 6001, tls: { enabled: true, certFile: '/x' } });

    expect(shown('/config/tls')).toBe(true);
    expect(last().config['tls']).toMatchObject({ enabled: true });
    expect(last().config['tls']).not.toHaveProperty('certFile');
    expect(el.querySelector('[data-pointer="/config/tls"] .sf-extras')).toBeNull();
  });

  it('tcp and udp: a json connection keeps decoderOptions through an edit', () => {
    const options = { records: 'ac', fields: { id: 'hex', latitude: 'lat', longitude: 'lon' } };

    for (const [driver, listen] of [
      ['tcp', { mode: 'listen' }],
      ['udp', {}],
    ] as const) {
      render(driver, { ...listen, port: 6001, format: 'json', decoderOptions: options });

      expect(jsonIn('/config/decoderOptions')).toEqual(options);
      expect(el.querySelector('fieldset[data-pointer="/config/decoderOptions"]')).toBeNull();
      expect(last().config['decoderOptions']).toEqual(options);

      setInput('input[name="port"]', '6002');
      expect(last().config['port']).toBe(6002);
      expect(last().config['decoderOptions']).toEqual(options);
      expect(last().valid).toBe(true);
    }
  });

  it('tcp and udp: an nmea-gps connection shows and keeps decoderOptions', () => {
    const options = { sourceEntityId: 'truck-1', name: 'Truck 1', entityType: 'VEHICLE' };

    for (const [driver, listen] of [
      ['tcp', { mode: 'listen' }],
      ['udp', {}],
    ] as const) {
      render(driver, { ...listen, port: 6001, format: 'nmea-gps', decoderOptions: options });

      expect(shown('/config/decoderOptions')).toBe(true);
      expect(jsonIn('/config/decoderOptions')).toEqual(options);
      expect(last().config['decoderOptions']).toEqual(options);

      setInput('input[name="port"]', '6002');
      expect(last().config['decoderOptions']).toEqual(options);
    }
  });

  it('tcp and udp: only the formats that read decoder options show them', () => {
    for (const driver of ['tcp', 'udp']) {
      const formats = schemaOf(driver).properties?.['format']?.enum ?? [];
      expect(formats.length).toBeGreaterThan(0);

      const withOptions = formats.filter((format) => {
        render(driver, { format });
        return shown('/config/decoderOptions');
      });
      expect(withOptions).toEqual(['cot', 'nmea-gps', 'json']);
    }
  });

  it('tcp: TLS secrets appear with TLS, clientCa only with client certificates', () => {
    const secrets = secretsOf('tcp');
    expect(secrets).toEqual(['tlsCert', 'tlsKey', 'clientCa', 'serverCa']);
    const shownSecrets = () => secrets.filter((name) => shown(`/secretRefs/${name}`));
    const set = (pointer: string, value: unknown) => {
      component.control(pointer)?.setValue(value);
      fixture.detectChanges();
    };
    const pem = 'file:/etc/sentinel/secrets/server.pem';
    const key = 'file:/etc/sentinel/secrets/server.key';
    const clientCa = 'file:/etc/sentinel/secrets/clients-ca.pem';

    // a listener without TLS has no certificate to present, and the Credentials section stays out of the way
    render('tcp', { mode: 'listen', port: 6001 }, secrets);
    expect(shownSecrets()).toEqual([]);
    expect(el.querySelector('.sf-section-secrets')).toBeNull();
    expect(last().valid).toBe(true);

    // with TLS it presents one, so it needs the certificate and its key
    set('/config/tls/enabled', true);
    expect(shownSecrets()).toEqual(['tlsCert', 'tlsKey']);
    expect(marked('/secretRefs/tlsCert')).toBe(true);
    expect(marked('/secretRefs/tlsKey')).toBe(true);
    expect(el.querySelector('[data-pointer="/secretRefs/tlsCert"] > label')?.textContent).toContain('TLS certificate (PEM)');
    expect(el.querySelector('input[name="tlsCert"]')?.getAttribute('placeholder')).toBe(pem);
    expect(el.querySelector('input[name="tlsKey"]')?.getAttribute('placeholder')).toBe(key);
    expect(last().valid).toBe(false);

    // asking the senders for certificates adds the CA that signs theirs
    set('/config/tls/requireClientCert', true);
    expect(shownSecrets()).toEqual(['tlsCert', 'tlsKey', 'clientCa']);
    expect(marked('/secretRefs/clientCa')).toBe(true);
    expect(el.querySelector('[data-pointer="/secretRefs/clientCa"] > label')?.textContent).toContain('Client CA (PEM)');
    expect(el.querySelector('input[name="clientCa"]')?.getAttribute('placeholder')).toBe(clientCa);
    setInput('input[name="tlsCert"]', pem);
    setInput('input[name="tlsKey"]', key);
    expect(last().valid).toBe(false);
    setInput('input[name="clientCa"]', clientCa);
    expect(last().valid).toBe(true);
    expect(last().secretRefs).toEqual({ tlsCert: pem, tlsKey: key, clientCa });

    // connecting, the certificate is only an option, the CA that matters is the remote's, and the listener's is gone
    set('/config/mode', 'connect');
    expect(shownSecrets()).toEqual(['tlsCert', 'tlsKey', 'serverCa']);
    for (const name of ['tlsCert', 'tlsKey', 'serverCa']) {
      expect(marked(`/secretRefs/${name}`)).toBe(false);
    }
    expect(el.querySelector('input[name="serverCa"]')?.getAttribute('placeholder')).toBe('file:/etc/sentinel/secrets/ca.pem');
    expect(last().secretRefs).toEqual({ tlsCert: pem, tlsKey: key });
    setInput('input[name="host"]', 'radar.example');
    expect(last().valid).toBe(true);

    // without TLS nothing is asked for, in either mode
    set('/config/tls/enabled', false);
    expect(shownSecrets()).toEqual([]);
    expect(last().secretRefs).toEqual({});
    set('/config/mode', 'listen');
    expect(shownSecrets()).toEqual([]);
  });

  it('tcp and udp: an nmea-gps connection needs decoderOptions, and no other format does', () => {
    for (const [driver, listen] of [
      ['tcp', { mode: 'listen' }],
      ['udp', {}],
    ] as const) {
      render(driver, { ...listen, port: 6001, format: 'nmea-gps' });

      expect(marked('/config/decoderOptions')).toBe(true);
      expect(last().valid).toBe(false);
      component.markAllTouched();
      fixture.detectChanges();
      expect(messagesOf('/config/decoderOptions')).toEqual(['Required.']);

      setInput('textarea[name="decoderOptions"]', '{"sourceEntityId": "truck-1"}');
      expect(last().valid).toBe(true);
      expect(messagesOf('/config/decoderOptions')).toEqual([]);

      // the formats that read options without needing them, and those that read none
      const others = (schemaOf(driver).properties?.['format']?.enum ?? []).filter((format) => format !== 'nmea-gps');
      expect(others.length).toBeGreaterThan(0);
      for (const format of others) {
        render(driver, { ...listen, port: 6001, format });
        expect(marked('/config/decoderOptions')).toBe(false);
        expect(last().valid).toBe(true);
      }
    }
  });

  it('tcp and udp: a server problem inside decoderOptions names the property it is about', () => {
    for (const [driver, listen] of [
      ['tcp', { mode: 'listen' }],
      ['udp', {}],
    ] as const) {
      // options without the platform: the server refuses at the property that is missing
      render(driver, { ...listen, port: 6001, format: 'nmea-gps', decoderOptions: { name: 'Truck 1' } });
      fixture.componentRef.setInput('problems', [
        { path: '/config/decoderOptions/sourceEntityId', message: 'is required' },
      ]);
      fixture.detectChanges();

      expect(serverMessagesOf('/config/decoderOptions')).toEqual(['sourceEntityId: is required']);
      expect(el.querySelector('.sf-unmatched')).toBeNull();

      // no options at all: the refusal is at the field itself, and reads as the server wrote it
      fixture.componentRef.setInput('problems', []); // one case's problems are gone before the next case's arrive
      render(driver, { ...listen, port: 6001, format: 'nmea-gps' });
      fixture.componentRef.setInput('problems', [{ path: '/config/decoderOptions', message: 'is required' }]);
      fixture.detectChanges();

      expect(serverMessagesOf('/config/decoderOptions')).toEqual(['is required']);
      expect(el.querySelector('.sf-unmatched')).toBeNull();
      fixture.componentRef.setInput('problems', []);
      fixture.detectChanges();
    }
  });

  // A hint is addressed by pointer and a field that is not shown has no place for one, so a schema that renames
  // or moves the port or the allowlist would silence the hints without a word. The pointers come from `listenOf`.
  it('tcp, udp and stream: the port and allowlist hints land on fields the schema shows', () => {
    const ports: ListenPorts = {
      min: 1024,
      max: 65535,
      reserved: [{ from: 3000, to: 3005 }],
      taken: [
        { protocol: 'tcp', port: 6001, feedId: 'radar', feedName: 'Radar feed', enabled: true },
        { protocol: 'udp', port: 10110, feedId: 'pier', feedName: 'Pier AIS', enabled: true },
      ],
      suggested: { tcp: 6002, udp: 10111 },
    };
    const listeners = [
      { driver: 'tcp', protocol: 'tcp', taken: 6001, config: (port?: number) => ({ mode: 'listen', port }) },
      { driver: 'udp', protocol: 'udp', taken: 10110, config: (port?: number) => ({ port }) },
      {
        driver: 'stream',
        protocol: 'tcp',
        taken: 6001,
        config: (port?: number) => ({ transport: { kind: 'tcpserver', address: port ? `:${port}` : undefined } }),
      },
      {
        driver: 'stream',
        protocol: 'udp',
        taken: 10110,
        config: (port?: number) => ({ transport: { kind: 'udp', address: port ? `:${port}` : undefined } }),
      },
    ] as const;

    for (const { driver, protocol, taken, config } of listeners) {
      // no port yet: a free one to take; a taken port: a warning, with a free one to take instead
      for (const stored of [config(), config(taken)]) {
        const hints = connectionHints({ driverId: driver, config: stored, ports });
        expect(hints.map((h) => h.path)).toEqual([listenOf(driver, stored)!.portPath, listenOf(driver, stored)!.allowPath]);

        render(driver, stored);
        fixture.componentRef.setInput('hints', hints);
        fixture.detectChanges();
        for (const hint of hints) {
          expect(el.querySelector(`[data-pointer="${hint.path}"] > .sf-hint`)?.textContent).toContain(hint.message);
        }

        // taking the port leaves a config the server reads as listening on it
        el.querySelector<HTMLButtonElement>(`[data-pointer="${hints[0].path}"] > .sf-hint button`)!.click();
        fixture.detectChanges();
        const port = listenOf(driver, last().config)?.port;
        expect(port).toBe(ports.suggested[protocol]);
        expect(connectionHints({ driverId: driver, config: last().config, ports }).map((h) => h.path)).toEqual([
          listenOf(driver, stored)!.allowPath,
        ]);
      }
    }
  });

  it('stream: keeps decoder.options and transport.allow through an edit', () => {
    const options = { records: 'ac', fields: { latitude: 'lat', longitude: 'lon' } };
    render('stream', {
      transport: { kind: 'tcpserver', address: ':4001', allow: ['10.0.0.0/8', '192.0.2.7'], maxConnections: 20 },
      framing: 'line',
      decoder: { name: 'json', options },
    });

    expect(jsonIn('/config/decoder/options')).toEqual(options);
    expect(el.querySelector('fieldset[data-pointer="/config/decoder/options"]')).toBeNull();
    expect(el.querySelector<HTMLInputElement>('[data-pointer="/config/transport/allow"] input')?.value).toBe(
      '10.0.0.0/8, 192.0.2.7',
    );

    setInput('input[name="address"]', ':4002');
    const config = last().config;
    expect(config['decoder']).toEqual({ name: 'json', options });
    expect(config['transport']).toMatchObject({
      kind: 'tcpserver',
      address: ':4002',
      allow: ['10.0.0.0/8', '192.0.2.7'],
      maxConnections: 20,
    });
    expect(last().valid).toBe(true);
  });

  it('stream: shows the listener settings only for the transports that listen', () => {
    const settings = ['allow', 'maxConnections', 'maxMessagesPerSec'];
    const shownFor = (kind: string, pointers: string[]) => {
      render('stream', { transport: { kind } });
      return pointers.filter((name) => shown(`/config/transport/${name}`));
    };

    expect(shownFor('tcpserver', settings)).toEqual(['allow', 'maxConnections', 'maxMessagesPerSec']);
    expect(shownFor('udp', settings)).toEqual(['allow', 'maxMessagesPerSec']);
    expect(shownFor('mqtt', settings)).toEqual([]);

    // mutual TLS is offered once TLS is on
    render('stream', { transport: { kind: 'tcpserver', tls: { enabled: false } } });
    expect(shown('/config/transport/tls/requireClientCert')).toBe(false);
    render('stream', { transport: { kind: 'tcpserver', tls: { enabled: true, requireClientCert: true } } });
    expect(shown('/config/transport/tls/requireClientCert')).toBe(true);
    expect((last().config['transport'] as { tls: Record<string, unknown> }).tls['requireClientCert']).toBe(true);
  });

  it('httpjson: keeps headers and an object body through an edit', () => {
    const headers = { 'X-Api-Key-Id': 'radar', Accept: 'application/json' };
    const body = { box: '{bbox}', limit: 500 };
    render('httpjson', {
      url: 'https://api.example.com/v2/search',
      method: 'POST',
      body,
      headers,
      decoder: { options: { records: 'ac', fields: { latitude: 'lat', longitude: 'lon' } } },
    });

    expect(jsonIn('/config/headers')).toEqual(headers);
    expect(jsonIn('/config/body')).toEqual(body);
    expect(el.querySelector('fieldset[data-pointer="/config/headers"]')).toBeNull();
    expect(el.querySelector('fieldset[data-pointer="/config/body"]')).toBeNull();

    setInput('input[name="intervalSec"]', '45');
    expect(last().config['intervalSec']).toBe(45);
    expect(last().config['headers']).toEqual(headers);
    expect(last().config['body']).toEqual(body);
    expect(last().valid).toBe(true);

    // a body may also be plain text, sent as it is
    setInput('textarea[name="body"]', 'q={lat},{lon}');
    expect(last().config['body']).toBe('q={lat},{lon}');
    expect(last().valid).toBe(true);
  });
});
