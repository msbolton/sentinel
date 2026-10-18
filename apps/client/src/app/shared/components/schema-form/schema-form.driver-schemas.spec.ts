import { readFileSync } from 'fs';
import { join } from 'path';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { SchemaFormChange, SchemaFormComponent } from './schema-form.component';
import { JsonSchema } from '../../models/feed.model';

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

  function render(driver: string, value: Record<string, unknown> = {}): void {
    fixture.componentRef.setInput('schema', schemaOf(driver));
    fixture.componentRef.setInput('value', value);
    fixture.detectChanges();
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
