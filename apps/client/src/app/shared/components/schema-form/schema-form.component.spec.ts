import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormGroup } from '@angular/forms';
import { FieldHint, SchemaFormChange, SchemaFormComponent } from './schema-form.component';
import { JsonSchema } from '../../models/feed.model';

const streamLikeSchema: JsonSchema = {
  type: 'object',
  required: ['transport', 'decoder'],
  properties: {
    name: { type: 'string', title: 'Label', minLength: 2 },
    baseUrl: { type: 'string', format: 'uri', title: 'API URL' },
    intervalSec: { type: 'integer', minimum: 1, maximum: 3600, default: 10, title: 'Poll interval (s)' },
    maxAreaSqDeg: { type: 'number', exclusiveMinimum: 0, default: 400 },
    viewportMode: { type: 'boolean', default: true, title: 'Viewport mode' },
    scope: { type: 'string', enum: ['mil', 'area'], default: 'mil', title: 'Scope' },
    kindConst: { type: 'string', const: 'fixed', title: 'Fixed kind' },
    groups: { type: 'array', items: { type: 'string' }, minItems: 2, title: 'Groups' },
    bbox: { type: 'string', pattern: '^$|^-?[0-9]+(,-?[0-9]+){3}$' },
    apiKey: { type: 'string', 'x-secret': true, title: 'API key' },
    transport: {
      type: 'object',
      title: 'Transport',
      required: ['kind'],
      properties: {
        kind: { type: 'string', enum: ['tcpclient', 'udp'], title: 'Kind' },
        address: { type: 'string', minLength: 1, title: 'Address' },
        tls: {
          type: 'object',
          title: 'TLS',
          properties: { enabled: { type: 'boolean', default: false }, serverName: { type: 'string' } },
        },
      },
    },
    decoder: {
      type: 'object',
      required: ['name'],
      properties: { name: { type: 'string', enum: ['json', 'sbs'] } },
    },
  },
};

describe('SchemaFormComponent', () => {
  let fixture: ComponentFixture<SchemaFormComponent>;
  let component: SchemaFormComponent;
  let el: HTMLElement;
  let changes: SchemaFormChange[];

  function last(): SchemaFormChange {
    return changes[changes.length - 1];
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

  function render(
    schema: JsonSchema,
    opts: { secretFields?: string[]; value?: Record<string, unknown>; secretRefs?: Record<string, string> } = {},
  ): void {
    fixture.componentRef.setInput('schema', schema);
    fixture.componentRef.setInput('secretFields', opts.secretFields ?? []);
    fixture.componentRef.setInput('value', opts.value ?? {});
    fixture.componentRef.setInput('secretRefs', opts.secretRefs ?? {});
    fixture.detectChanges();
  }

  /** The server messages shown on the control or group at `pointer`, not on the fields inside it. */
  function serverMessages(pointer: string): string[] {
    return Array.from(el.querySelectorAll(`[data-pointer="${pointer}"] > .sf-error-server`)).map(
      (n) => n.textContent?.trim() ?? '',
    );
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({ imports: [SchemaFormComponent] }).compileComponents();
    fixture = TestBed.createComponent(SchemaFormComponent);
    component = fixture.componentInstance;
    el = fixture.nativeElement as HTMLElement;
    changes = [];
    component.changed.subscribe((c) => changes.push(c));
  });

  it('builds controls for each schema kind', () => {
    render(streamLikeSchema);

    expect(el.querySelector('input[name="name"]')?.getAttribute('type')).toBe('text');
    expect(el.querySelector('input[name="baseUrl"]')?.getAttribute('type')).toBe('url');

    const interval = el.querySelector<HTMLInputElement>('input[name="intervalSec"]');
    expect(interval?.type).toBe('number');
    expect(interval?.getAttribute('step')).toBe('1');
    expect(interval?.getAttribute('placeholder')).toBe('10');

    expect(el.querySelector('input[name="maxAreaSqDeg"]')?.getAttribute('step')).toBe('any');

    const viewport = el.querySelector<HTMLInputElement>('input[name="viewportMode"]');
    expect(viewport?.type).toBe('checkbox');
    expect(viewport?.checked).toBe(true);

    const scope = el.querySelector<HTMLSelectElement>('select[name="scope"]');
    expect(scope).toBeTruthy();
    expect(Array.from(scope!.options).map((o) => o.textContent?.trim())).toEqual(['Default (mil)', 'mil', 'area']);

    const konst = el.querySelector<HTMLInputElement>('input[name="kindConst"]');
    expect(konst?.readOnly).toBe(true);
    expect(konst?.value).toBe('fixed');

    expect(el.querySelector('input[name="groups"]')?.getAttribute('type')).toBe('text');

    const transport = el.querySelector('fieldset[data-pointer="/config/transport"]');
    expect(transport?.querySelector('legend')?.textContent).toContain('Transport');
    expect(transport?.querySelector('select[name="kind"]')).toBeTruthy();
    expect(transport?.querySelector('fieldset[data-pointer="/config/transport/tls"] input[name="enabled"]')).toBeTruthy();

    const labels = Array.from(el.querySelectorAll('label')).map((l) => l.textContent?.trim());
    expect(labels).toContain('Poll interval (s)');
    expect(labels).toContain('Max area sq deg');
  });

  it('emits once after build with defaults and constants applied', () => {
    render(streamLikeSchema);

    expect(changes.length).toBe(1);
    expect(last().config).toEqual({
      viewportMode: true,
      kindConst: 'fixed',
      transport: { tls: { enabled: false } },
    });
    expect(last().secretRefs).toEqual({});
    expect(last().valid).toBe(false);
  });

  it('applies required, min, max and exclusiveMinimum validators', () => {
    render(streamLikeSchema);

    expect(component.control('/config/transport/kind')?.hasError('required')).toBe(true);
    expect(component.control('/config/decoder/name')?.hasError('required')).toBe(true);

    setInput('input[name="intervalSec"]', '0');
    expect(component.control('/config/intervalSec')?.hasError('min')).toBe(true);
    expect(last().valid).toBe(false);

    setInput('input[name="intervalSec"]', '4000');
    expect(component.control('/config/intervalSec')?.hasError('max')).toBe(true);

    setInput('input[name="intervalSec"]', '2.5');
    expect(component.control('/config/intervalSec')?.hasError('integer')).toBe(true);

    setInput('input[name="intervalSec"]', '15');
    expect(component.control('/config/intervalSec')?.valid).toBe(true);
    expect(last().config['intervalSec']).toBe(15);

    setInput('input[name="maxAreaSqDeg"]', '0');
    expect(component.control('/config/maxAreaSqDeg')?.hasError('exclusiveMinimum')).toBe(true);

    setInput('input[name="name"]', 'a');
    expect(component.control('/config/name')?.hasError('minlength')).toBe(true);

    setInput('input[name="bbox"]', 'nope');
    expect(component.control('/config/bbox')?.hasError('pattern')).toBe(true);
    setInput('input[name="bbox"]', '1,2,3,4');
    expect(component.control('/config/bbox')?.valid).toBe(true);

    component.control('/config/transport/kind')?.setValue('udp');
    component.control('/config/decoder/name')?.setValue('json');
    component.control('/config/maxAreaSqDeg')?.setValue(5);
    component.control('/config/name')?.setValue('ok');
    fixture.detectChanges();
    expect(last().valid).toBe(true);
    expect(last().config['transport']).toEqual({ kind: 'udp', tls: { enabled: false } });
  });

  it('shows client-side messages once a field is touched', () => {
    render(streamLikeSchema);
    setInput('input[name="intervalSec"]', '0');

    const field = el.querySelector('[data-pointer="/config/intervalSec"]');
    expect(field?.classList.contains('has-issue')).toBe(true);
    expect(field?.querySelector('.sf-error')?.textContent).toContain('at least 1');
  });

  it('turns comma separated text into a string array and honours minItems', () => {
    render(streamLikeSchema);

    setInput('input[name="groups"]', 'active');
    expect(component.control('/config/groups')?.hasError('minItems')).toBe(true);

    setInput('input[name="groups"]', ' active , stations,, ');
    expect(component.control('/config/groups')?.valid).toBe(true);
    expect(last().config['groups']).toEqual(['active', 'stations']);

    setInput('input[name="groups"]', '');
    expect(component.control('/config/groups')?.valid).toBe(true);
    expect(last().config['groups']).toBeUndefined();
  });

  it('keeps secrets in secretRefs, never in config', () => {
    render(streamLikeSchema, { secretFields: ['clientSecret'] });

    const credentials = el.querySelector('.sf-section-secrets');
    expect(credentials?.querySelector('.sf-section-title')?.textContent).toContain('Credentials');
    expect(credentials?.querySelector('input[name="clientSecret"]')?.getAttribute('placeholder')).toBe('env:CLIENT_SECRET');
    expect(credentials?.querySelector('input[name="apiKey"]')?.getAttribute('placeholder')).toBe('env:API_KEY');
    expect(el.querySelector('.sf-section-driver input[name="apiKey"]')).toBeNull();

    // without x-secrets a label is the property's title, else the humanized name
    expect(credentials?.querySelector('label[for="sf-secret-clientSecret"]')?.textContent?.trim()).toBe('Client secret');
    expect(credentials?.querySelector('label[for="sf-secret-apiKey"]')?.textContent?.trim()).toBe('API key');
    // the intro offers a mounted file as well as an environment variable
    expect(credentials?.querySelector('.sf-help')?.textContent).toContain('file:/etc/sentinel/');

    setInput('input[name="clientSecret"]', 'plain-text-secret');
    expect(component.control('/secretRefs/clientSecret')?.hasError('pattern')).toBe(true);
    expect(last().valid).toBe(false);

    setInput('input[name="clientSecret"]', 'env:OPENSKY_CLIENT_SECRET');
    setInput('input[name="apiKey"]', 'env:API_KEY');
    expect(component.control('/secretRefs/clientSecret')?.valid).toBe(true);
    expect(last().secretRefs).toEqual({ clientSecret: 'env:OPENSKY_CLIENT_SECRET', apiKey: 'env:API_KEY' });
    expect(last().config['apiKey']).toBeUndefined();
    expect(last().config['clientSecret']).toBeUndefined();
  });

  it('omits empty optional fields from the emitted config', () => {
    render(streamLikeSchema, {
      value: { intervalSec: 30, name: 'hello', scope: 'area', transport: { kind: 'udp', address: ':4001' } },
    });

    expect(last().config).toEqual({
      name: 'hello',
      intervalSec: 30,
      viewportMode: true,
      scope: 'area',
      kindConst: 'fixed',
      transport: { kind: 'udp', address: ':4001', tls: { enabled: false } },
    });

    setInput('input[name="name"]', '   ');
    setInput('input[name="intervalSec"]', '');
    component.control('/config/scope')?.setValue('');
    component.control('/config/transport/address')?.setValue('');
    fixture.detectChanges();

    const config = last().config;
    expect('name' in config).toBe(false);
    expect('intervalSec' in config).toBe(false);
    expect('scope' in config).toBe(false);
    expect('baseUrl' in config).toBe(false);
    expect('groups' in config).toBe(false);
    expect(config['transport']).toEqual({ kind: 'udp', tls: { enabled: false } });
  });

  it('prefills from the initial value and secretRefs', () => {
    render(streamLikeSchema, {
      secretFields: ['clientSecret'],
      value: { groups: ['a', 'b'], viewportMode: false, transport: { kind: 'tcpclient', tls: { enabled: true, serverName: 'x' } } },
      secretRefs: { clientSecret: 'env:S' },
    });

    expect(el.querySelector<HTMLInputElement>('input[name="groups"]')?.value).toBe('a, b');
    expect(el.querySelector<HTMLInputElement>('input[name="viewportMode"]')?.checked).toBe(false);
    expect(el.querySelector<HTMLInputElement>('input[name="clientSecret"]')?.value).toBe('env:S');
    expect(component.control('/config/transport/kind')?.value).toBe('tcpclient');
    expect(last().config['transport']).toEqual({ kind: 'tcpclient', tls: { enabled: true, serverName: 'x' } });
    expect(last().secretRefs).toEqual({ clientSecret: 'env:S' });
  });

  it('renders the common options under Advanced and merges them into config', () => {
    render(streamLikeSchema);

    const advanced = el.querySelector('.sf-section-advanced');
    expect(advanced).toBeTruthy();
    expect(advanced?.querySelector('.sf-section-body')).toBeNull();

    advanced?.querySelector<HTMLButtonElement>('.sf-section-toggle')?.click();
    fixture.detectChanges();

    expect(advanced?.querySelector('input[name="staleWarnSec"]')).toBeTruthy();
    expect(advanced?.querySelector('select[name="backpressure"]')).toBeTruthy();
    expect(advanced?.querySelector('select[name="rawPayload"]')).toBeTruthy();

    setInput('input[name="staleWarnSec"]', '120');
    component.control('/config/backpressure')?.setValue('drop');
    fixture.detectChanges();

    expect(last().config['staleWarnSec']).toBe(120);
    expect(last().config['backpressure']).toBe('drop');
    expect(last().config['rawPayload']).toBeUndefined();
  });

  it('opens Advanced when the initial value sets a common option', () => {
    render(streamLikeSchema, { value: { pinnedReplica: 'ingest-0' } });

    expect(component.advancedOpen()).toBe(true);
    expect(el.querySelector<HTMLInputElement>('.sf-section-advanced input[name="pinnedReplica"]')?.value).toBe('ingest-0');
  });

  it('maps server problems to the matching field, group or secret', () => {
    render(streamLikeSchema, { secretFields: ['clientSecret'] });

    fixture.componentRef.setInput('problems', [
      { path: '/config/intervalSec', message: 'must be >= 1' },
      { path: '/config/transport/kind', message: 'unknown kind' },
      { path: '/config/transport/address', message: 'is required' },
      { path: '/config/transport/tls', message: "additional properties 'certFile' not allowed" },
      { path: '/config/groups/1', message: 'must be non-empty' },
      { path: '/secretRefs/clientSecret', message: 'env var not set' },
      { path: '/config/staleWarnSec', message: 'too low' },
      { path: '/config/nope', message: 'unknown property' },
    ]);
    fixture.detectChanges();

    const text = (pointer: string) =>
      Array.from(el.querySelectorAll(`[data-pointer="${pointer}"] > .sf-error-server`)).map((n) => n.textContent?.trim());

    expect(text('/config/intervalSec')).toEqual(['must be >= 1']);
    expect(text('/config/transport/kind')).toEqual(['unknown kind']);
    // the address field has its own control, so the group around it says nothing about it
    expect(text('/config/transport/address')).toEqual(['is required']);
    expect(text('/config/transport/tls')).toEqual(["additional properties 'certFile' not allowed"]);
    expect(text('/config/transport')).toEqual([]);
    expect(text('/config/groups')).toEqual(['must be non-empty']);
    expect(text('/secretRefs/clientSecret')).toEqual(['env var not set']);

    // advanced opened itself because one of its fields has a problem
    expect(component.advancedOpen()).toBe(true);
    expect(text('/config/staleWarnSec')).toEqual(['too low']);

    const unmatched = el.querySelector('.sf-unmatched');
    expect(unmatched?.textContent).toContain('/config/nope');
    expect(unmatched?.textContent).toContain('unknown property');
  });

  // The schema leaves out `outbound`, so the Delivery tuning group is one of the common options, under Advanced.
  describe('a server problem in the Advanced section', () => {
    it('opens it for a problem on a field inside one of its groups', () => {
      render(streamLikeSchema);
      expect(component.advancedOpen()).toBe(false);

      fixture.componentRef.setInput('problems', [
        { path: '/config/outbound/queueMax', message: 'minimum: got 5, want 100' },
      ]);
      fixture.detectChanges();

      expect(component.advancedOpen()).toBe(true);
      expect(serverMessages('/config/outbound/queueMax')).toEqual(['minimum: got 5, want 100']);
      expect(el.querySelector('.sf-unmatched')).toBeNull();
    });

    it('opens it for a problem on one of its groups as a whole', () => {
      render(streamLikeSchema);

      fixture.componentRef.setInput('problems', [
        { path: '/config/outbound', message: "additional properties 'retired' not allowed" },
      ]);
      fixture.detectChanges();

      expect(component.advancedOpen()).toBe(true);
      expect(serverMessages('/config/outbound')).toEqual(["additional properties 'retired' not allowed"]);
    });

    it('stays closed for a problem on a field outside it', () => {
      render(streamLikeSchema);

      fixture.componentRef.setInput('problems', [{ path: '/config/intervalSec', message: 'must be >= 1' }]);
      fixture.detectChanges();

      expect(serverMessages('/config/intervalSec')).toEqual(['must be >= 1']);
      expect(component.advancedOpen()).toBe(false);
    });
  });

  it('disables and re-enables every control', () => {
    render(streamLikeSchema, { secretFields: ['clientSecret'] });

    fixture.componentRef.setInput('disabled', true);
    fixture.detectChanges();
    expect(el.querySelector<HTMLInputElement>('input[name="intervalSec"]')?.disabled).toBe(true);
    expect(el.querySelector<HTMLInputElement>('input[name="clientSecret"]')?.disabled).toBe(true);

    fixture.componentRef.setInput('disabled', false);
    fixture.detectChanges();
    expect(el.querySelector<HTMLInputElement>('input[name="intervalSec"]')?.disabled).toBe(false);
  });

  it('leaves the keys the parent owns out of the form and the emitted config', () => {
    fixture.componentRef.setInput('externalKeys', ['classification', 'policy', 'pinnedReplica']);
    render(
      {
        type: 'object',
        properties: {
          intervalSec: { type: 'integer' },
          classification: { type: 'string', enum: ['UNCLASSIFIED', 'SECRET'] },
        },
      },
      { value: { intervalSec: 5, classification: 'SECRET', policy: { entityTypes: ['AIRCRAFT'] }, pinnedReplica: 'ingest-0' } },
    );

    expect(el.querySelector('input[name="intervalSec"]')).toBeTruthy();
    expect(el.querySelector('select[name="classification"]')).toBeNull();
    expect(el.querySelector('[data-pointer="/config/policy"]')).toBeNull();
    expect(el.querySelector('input[name="pinnedReplica"]')).toBeNull();
    expect(component.advancedOpen()).toBe(false);
    expect(last().config).toEqual({ intervalSec: 5 });
    expect(component.control('/config/classification')).toBeNull();
  });

  it('rebuilds when the schema changes', () => {
    render(streamLikeSchema);
    render({ type: 'object', properties: { only: { type: 'string' } } });

    expect(el.querySelector('input[name="only"]')).toBeTruthy();
    expect(el.querySelector('input[name="intervalSec"]')).toBeNull();
    expect(last().config).toEqual({});
    expect(last().valid).toBe(true);
  });

  describe('x-showWhen', () => {
    const showWhenSchema: JsonSchema = {
      type: 'object',
      required: ['mode'],
      properties: {
        mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen' },
        host: { type: 'string', title: 'Host', 'x-showWhen': { mode: 'connect' } },
        allow: { type: 'array', items: { type: 'string' }, title: 'Allowed senders', 'x-showWhen': { mode: ['listen'] } },
        tls: {
          type: 'object',
          properties: {
            enabled: { type: 'boolean' },
            requireClientCert: { type: 'boolean', 'x-showWhen': { enabled: true } },
          },
        },
      },
    };

    function setSelect(selector: string, value: string): void {
      const select = el.querySelector<HTMLSelectElement>(selector);
      if (!select) {
        throw new Error(`no select for ${selector}`);
      }
      select.value = Array.from(select.options).find((o) => o.textContent?.trim() === value)!.value;
      select.dispatchEvent(new Event('change'));
      fixture.detectChanges();
    }

    it('shows a field only while its sibling has one of the values, and leaves hidden values out', () => {
      render(showWhenSchema, { value: { mode: 'listen', host: 'stale.example' } });
      expect(el.querySelector('[data-pointer="/config/host"]')).toBeNull();
      expect(el.querySelector('[data-pointer="/config/allow"]')).not.toBeNull();
      expect(last().config['host']).toBeUndefined();

      component.configGroup.get('mode')!.setValue('connect');
      fixture.detectChanges();
      expect(el.querySelector('[data-pointer="/config/host"]')).not.toBeNull();
      expect(el.querySelector('[data-pointer="/config/allow"]')).toBeNull();
      expect(last().config['host']).toBe('stale.example');
    });

    it('re-evaluates when the user picks a value in the form', () => {
      render(showWhenSchema, { value: { mode: 'listen' } });
      expect(el.querySelector('input[name="host"]')).toBeNull();

      setSelect('select[name="mode"]', 'connect');
      expect(el.querySelector('input[name="host"]')).not.toBeNull();
      expect(el.querySelector('input[name="allow"]')).toBeNull();
    });

    it('works inside groups, and leaves hidden nested values out', () => {
      render(showWhenSchema, { value: { mode: 'listen', tls: { requireClientCert: true } } });
      expect(el.querySelector('[data-pointer="/config/tls/requireClientCert"]')).toBeNull();
      expect(last().config['tls']).toEqual({ enabled: false });

      (component.configGroup.get('tls') as FormGroup).get('enabled')!.setValue(true);
      fixture.detectChanges();
      expect(el.querySelector('[data-pointer="/config/tls/requireClientCert"]')).not.toBeNull();
      expect(last().config['tls']).toEqual({ enabled: true, requireClientCert: true });
    });

    it('treats an empty sibling as its schema default', () => {
      // Optional and left unset, so the select stays empty and the default, listen, applies.
      // A required mode would start at listen as a value and never reach this fallback.
      render({ ...showWhenSchema, required: [] });
      expect(component.control('/config/mode')?.value).toBe('');
      expect(el.querySelector('[data-pointer="/config/host"]')).toBeNull();
      expect(el.querySelector('[data-pointer="/config/allow"]')).not.toBeNull();
    });

    it('hides a field whose empty sibling has no default', () => {
      render({
        type: 'object',
        properties: {
          format: { type: 'string', enum: ['sentinel', 'json'] },
          encoding: { type: 'string', enum: ['protobuf', 'json'], 'x-showWhen': { format: 'sentinel' } },
        },
      });
      expect(el.querySelector('[data-pointer="/config/encoding"]')).toBeNull();
    });

    it('does not let a hidden required field block validity', () => {
      render(
        { ...showWhenSchema, required: ['mode', 'host'] },
        { value: { mode: 'listen' } },
      );
      expect(last().valid).toBe(true);

      component.configGroup.get('mode')!.setValue('connect');
      fixture.detectChanges();
      expect(last().valid).toBe(false);

      component.configGroup.get('host')!.setValue('radar.example');
      fixture.detectChanges();
      expect(last().valid).toBe(true);
    });

    it('hides a whole group, its children included, from the form, the config and validity', () => {
      render(
        {
          type: 'object',
          properties: {
            format: { type: 'string', enum: ['sentinel', 'json'], default: 'sentinel' },
            decoderOptions: {
              type: 'object',
              required: ['timeField'],
              properties: { timeField: { type: 'string' } },
              'x-showWhen': { format: 'json' },
            },
          },
        },
        { value: { decoderOptions: { timeField: 'ts' } } },
      );
      expect(el.querySelector('[data-pointer="/config/decoderOptions"]')).toBeNull();
      expect(last().config['decoderOptions']).toBeUndefined();
      expect(last().valid).toBe(true);

      component.configGroup.get('format')!.setValue('json');
      fixture.detectChanges();
      expect(el.querySelector('[data-pointer="/config/decoderOptions"]')).not.toBeNull();
      expect(last().config['decoderOptions']).toEqual({ timeField: 'ts' });
    });
  });

  describe('allOf conditions', () => {
    // Like tcp: a port is always needed, a listener's must be unprivileged, and connecting needs a host too.
    const tcpLikeSchema: JsonSchema = {
      type: 'object',
      required: ['mode'],
      properties: {
        mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen', title: 'Mode' },
        host: { type: 'string', minLength: 1, title: 'Host', 'x-showWhen': { mode: 'connect' } },
        port: { type: 'integer', minimum: 1, maximum: 65535, title: 'Port' },
      },
      allOf: [
        {
          if: { properties: { mode: { const: 'listen' } }, required: ['mode'] },
          then: { required: ['port'], properties: { port: { minimum: 1024 } } },
        },
        { if: { properties: { mode: { const: 'connect' } }, required: ['mode'] }, then: { required: ['host', 'port'] } },
      ],
    };

    // Like stream: what a transport needs depends on its kind, which has no default.
    const transportOf = (required: string[]): JsonSchema => ({
      type: 'object',
      properties: {
        transport: {
          type: 'object',
          required,
          properties: {
            kind: { type: 'string', enum: ['tcpclient', 'udp', 'websocket'], title: 'Kind' },
            address: { type: 'string', minLength: 1, title: 'Address' },
            url: { type: 'string', title: 'WebSocket URL' },
          },
          allOf: [
            { if: { properties: { kind: { const: 'tcpclient' } }, required: ['kind'] }, then: { required: ['address'] } },
            { if: { properties: { kind: { const: 'udp' } }, required: ['kind'] }, then: { required: ['address'] } },
            { if: { properties: { kind: { const: 'websocket' } }, required: ['kind'] }, then: { required: ['url'] } },
          ],
        },
      },
    });

    /** The client-side messages a field shows. */
    function messagesOf(pointer: string): Array<string | undefined> {
      return Array.from(el.querySelectorAll(`[data-pointer="${pointer}"] > .sf-error`)).map((n) => n.textContent?.trim());
    }

    /** Whether the label (the legend, for a group) of a field carries the required asterisk. */
    function marked(pointer: string): boolean {
      const label = `[data-pointer="${pointer}"] > label .sf-required, [data-pointer="${pointer}"] > legend .sf-required`;
      return el.querySelector(label) !== null;
    }

    it('requires port in listen mode, host and port in connect mode', () => {
      render(tcpLikeSchema);

      // listening needs a port and no host
      expect(marked('/config/port')).toBe(true);
      expect(el.querySelector('[data-pointer="/config/host"]')).toBeNull();
      expect(last().valid).toBe(false);

      setInput('input[name="port"]', '6001');
      expect(last().valid).toBe(true);

      // connecting needs both, and says so once the form is submitted
      render(tcpLikeSchema, { value: { mode: 'connect' } });
      expect(marked('/config/host')).toBe(true);
      expect(marked('/config/port')).toBe(true);
      expect(messagesOf('/config/host')).toEqual([]);
      expect(last().valid).toBe(false);

      component.markAllTouched();
      fixture.detectChanges();
      expect(messagesOf('/config/host')).toEqual(['Required.']);
      expect(messagesOf('/config/port')).toEqual(['Required.']);

      setInput('input[name="port"]', '6001');
      expect(last().valid).toBe(false);
      setInput('input[name="host"]', 'radar.example');
      expect(last().valid).toBe(true);
      expect(messagesOf('/config/host')).toEqual([]);
    });

    it('applies a conditional minimum only while its condition holds', () => {
      render(tcpLikeSchema);

      setInput('input[name="port"]', '80');
      expect(messagesOf('/config/port')).toEqual(['Must be at least 1024.']);
      expect(last().valid).toBe(false);

      // below the schema's own minimum as well, there is still one message
      setInput('input[name="port"]', '0');
      expect(messagesOf('/config/port')).toEqual(['Must be at least 1024.']);

      setInput('input[name="port"]', '1024');
      expect(messagesOf('/config/port')).toEqual([]);
      expect(last().valid).toBe(true);

      // a remote port below 1024 is fine to connect to
      render(tcpLikeSchema, { value: { mode: 'connect', host: 'radar.example', port: 80 } });
      expect(messagesOf('/config/port')).toEqual([]);
      expect(last().valid).toBe(true);

      // and the minimum is back with its condition
      component.control('/config/mode')?.setValue('listen');
      component.markAllTouched();
      fixture.detectChanges();
      expect(messagesOf('/config/port')).toEqual(['Must be at least 1024.']);
      expect(last().valid).toBe(false);
    });

    it('applies a conditional maximum the same way', () => {
      render(
        {
          type: 'object',
          properties: {
            mode: { type: 'string', enum: ['fast', 'slow'], default: 'fast' },
            rate: { type: 'integer', minimum: 1 },
          },
          allOf: [{ if: { properties: { mode: { enum: ['slow'] } } }, then: { properties: { rate: { maximum: 10 } } } }],
        },
        { value: { rate: 50 } },
      );
      expect(last().valid).toBe(true);

      component.control('/config/mode')?.setValue('slow');
      component.markAllTouched();
      fixture.detectChanges();
      expect(messagesOf('/config/rate')).toEqual(['Must be at most 10.']);
      expect(last().valid).toBe(false);
    });

    it('leaves a rule alone while its sibling is empty with no default', () => {
      render(transportOf(['kind']), { value: { transport: { kind: '' } } });

      expect(component.control('/config/transport/kind')?.value).toBe('');
      expect(marked('/config/transport/address')).toBe(false);
      expect(marked('/config/transport/url')).toBe(false);

      component.markAllTouched();
      fixture.detectChanges();
      expect(messagesOf('/config/transport/kind')).toEqual(['Required.']);
      expect(messagesOf('/config/transport/address')).toEqual([]);

      // once a kind is picked, its rule applies
      component.control('/config/transport/kind')?.setValue('tcpclient');
      fixture.detectChanges();
      expect(marked('/config/transport/address')).toBe(true);
      expect(marked('/config/transport/url')).toBe(false);
      expect(messagesOf('/config/transport/address')).toEqual(['Required.']);
      expect(last().valid).toBe(false);

      setInput('input[name="address"]', ':4001');
      expect(last().valid).toBe(true);

      // where nothing else needs the kind, leaving it empty needs nothing at all
      render(transportOf([]));
      expect(last().valid).toBe(true);
    });

    it('ignores conditions it cannot read', () => {
      // the server sends whatever its schemas say, which the type does not cover: anyOf, a string where a list belongs
      const unreadable = {
        type: 'object',
        properties: {
          mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen' },
          secure: { type: 'boolean' },
          port: { type: 'integer' },
          host: { type: 'string' },
        },
        allOf: [
          // an if that tests two properties
          { if: { properties: { mode: { const: 'listen' }, secure: { const: true } } }, then: { required: ['port'] } },
          // an entry with no if and then
          { anyOf: [{ required: ['port'] }, { required: ['host'] }] },
          // an if that is not a plain property test
          {
            if: { anyOf: [{ properties: { mode: { const: 'listen' } } }, { properties: { secure: { const: true } } }] },
            then: { required: ['host'] },
          },
          // an if that tests a pattern, not a value
          { if: { properties: { mode: { pattern: '^l' } } }, then: { required: ['host'] } },
          // an if that also needs something else to be present
          { if: { properties: { mode: { const: 'listen' } }, required: ['host'] }, then: { required: ['port'] } },
          // an if that says more about the same value than the form can follow
          { if: { properties: { mode: { const: 'listen' } }, not: { required: ['host'] } }, then: { required: ['port'] } },
          // a then that is not a list of names
          { if: { properties: { mode: { const: 'listen' } } }, then: { required: 'port' } },
        ],
      } as unknown as JsonSchema;

      expect(() => render(unreadable)).not.toThrow();
      expect(el.querySelector('.sf-required')).toBeNull();
      expect(last().valid).toBe(true);

      component.markAllTouched();
      fixture.detectChanges();
      expect(el.querySelector('.sf-error')).toBeNull();
    });

    it('reads a rule whose then also constrains a nested object, and requires what it names', () => {
      // as the tcp and udp schemas carry it: the then holds a nested required and a minLength the form cannot check
      const nmeaGps: JsonSchema = {
        if: { properties: { format: { const: 'nmea-gps' } }, required: ['format'] },
        then: {
          required: ['decoderOptions'],
          properties: {
            decoderOptions: { required: ['sourceEntityId'], properties: { sourceEntityId: { type: 'string', minLength: 1 } } },
          },
        },
      };
      const schema: JsonSchema = {
        type: 'object',
        properties: {
          format: { type: 'string', enum: ['sentinel', 'json', 'nmea-gps', 'cot'], default: 'sentinel', title: 'Format' },
          decoderOptions: {
            type: 'object',
            title: 'Decoder options',
            'x-showWhen': { format: ['json', 'nmea-gps', 'cot'] },
          },
        },
        allOf: [nmeaGps],
      };

      expect(() => render(schema, { value: { format: 'json' } })).not.toThrow();
      expect(marked('/config/decoderOptions')).toBe(false);
      expect(last().valid).toBe(true);

      render(schema, { value: { format: 'nmea-gps' } });
      expect(marked('/config/decoderOptions')).toBe(true);
      expect(last().valid).toBe(false);

      component.markAllTouched();
      fixture.detectChanges();
      expect(messagesOf('/config/decoderOptions')).toEqual(['Required.']);

      // text that is not a JSON object has a message of its own, and is not also reported as missing
      setInput('textarea[name="decoderOptions"]', '{"sourceEntityId":');
      expect(messagesOf('/config/decoderOptions')).toEqual(['Not valid JSON.']);
      expect(last().valid).toBe(false);

      setInput('textarea[name="decoderOptions"]', '[1]');
      expect(messagesOf('/config/decoderOptions')).toEqual(['Must be a JSON object, like {"key": "value"}.']);
      expect(last().valid).toBe(false);

      // an empty object is a valid object that says nothing, so it is still missing
      setInput('textarea[name="decoderOptions"]', '{}');
      expect(messagesOf('/config/decoderOptions')).toEqual(['Required.']);
      expect(last().valid).toBe(false);

      // what is inside the object is the server's to check
      setInput('textarea[name="decoderOptions"]', '{"name": "Truck 1"}');
      expect(last().valid).toBe(true);
      expect(messagesOf('/config/decoderOptions')).toEqual([]);

      component.control('/config/format')?.setValue('cot');
      fixture.detectChanges();
      expect(marked('/config/decoderOptions')).toBe(false);
    });

    it('does not let a rule require a field that x-showWhen hides', () => {
      render({
        type: 'object',
        properties: {
          mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen' },
          secure: { type: 'boolean', title: 'Secure' },
          certFile: { type: 'string', title: 'Certificate', 'x-showWhen': { secure: true } },
        },
        allOf: [{ if: { properties: { mode: { const: 'listen' } } }, then: { required: ['certFile'] } }],
      });

      // the rule holds, but the field is out of sight and out of the config
      expect(el.querySelector('[data-pointer="/config/certFile"]')).toBeNull();
      expect(last().valid).toBe(true);

      component.control('/config/secure')?.setValue(true);
      fixture.detectChanges();
      expect(marked('/config/certFile')).toBe(true);
      expect(last().valid).toBe(false);

      setInput('input[name="certFile"]', 'server.pem');
      expect(last().valid).toBe(true);

      component.control('/config/secure')?.setValue(false);
      fixture.detectChanges();
      expect(last().valid).toBe(true);
      expect(last().config['certFile']).toBeUndefined();
    });

    it('asks for a group that a rule requires', () => {
      render(
        {
          type: 'object',
          properties: {
            mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen' },
            login: {
              type: 'object',
              title: 'Login',
              properties: { username: { type: 'string' }, token: { type: 'string' } },
            },
          },
          allOf: [{ if: { properties: { mode: { const: 'connect' } } }, then: { required: ['login'] } }],
        },
        { value: { mode: 'connect' } },
      );

      expect(marked('/config/login')).toBe(true);
      expect(last().valid).toBe(false);

      component.markAllTouched();
      fixture.detectChanges();
      expect(messagesOf('/config/login')).toEqual(['Required.']);

      setInput('input[name="username"]', 'radar');
      expect(last().valid).toBe(true);
      expect(messagesOf('/config/login')).toEqual([]);

      // listening leaves the group optional
      component.control('/config/mode')?.setValue('listen');
      fixture.detectChanges();
      expect(marked('/config/login')).toBe(false);
    });

    it('x-showWhen with several keys needs all of them', () => {
      const schema: JsonSchema = {
        type: 'object',
        properties: {
          mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen' },
          secure: { type: 'boolean', title: 'Secure' },
          clientCa: { type: 'string', title: 'Client CA', 'x-showWhen': { mode: 'listen', secure: true } },
        },
      };
      render(schema, { value: { clientCa: 'ca.pem' } });
      const shown = () => el.querySelector('[data-pointer="/config/clientCa"]') !== null;

      expect(shown()).toBe(false); // listen, but not secure
      expect(last().config['clientCa']).toBeUndefined();

      component.control('/config/secure')?.setValue(true);
      fixture.detectChanges();
      expect(shown()).toBe(true);
      expect(last().config['clientCa']).toBe('ca.pem');

      component.control('/config/mode')?.setValue('connect');
      fixture.detectChanges();
      expect(shown()).toBe(false); // secure, but not listening
      expect(last().config['clientCa']).toBeUndefined();

      component.control('/config/secure')?.setValue(false);
      fixture.detectChanges();
      expect(shown()).toBe(false); // neither
    });
  });

  describe('x-secrets', () => {
    // Like tcp: which certificates a connection needs depends on its mode and on its TLS settings.
    const tlsSchema: JsonSchema = {
      type: 'object',
      required: ['mode'],
      properties: {
        mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen' },
        tls: {
          type: 'object',
          properties: {
            enabled: { type: 'boolean', default: false },
            requireClientCert: { type: 'boolean', default: false, 'x-showWhen': { enabled: true } },
          },
        },
      },
      'x-secrets': {
        tlsCert: {
          title: 'TLS certificate (PEM)',
          description: 'listen: the certificate Sentinel presents. connect: an optional client certificate.',
          examples: ['file:/etc/sentinel/secrets/server.pem'],
          showWhen: { 'tls/enabled': true },
          requiredWhen: { mode: 'listen', 'tls/enabled': true },
        },
        tlsKey: {
          title: 'TLS private key (PEM)',
          examples: ['file:/etc/sentinel/secrets/server.key'],
          showWhen: { 'tls/enabled': true },
          requiredWhen: { mode: 'listen', 'tls/enabled': true },
        },
        clientCa: {
          title: 'Client CA (PEM)',
          description: "Signs the senders' certificates.",
          examples: ['file:/etc/sentinel/secrets/clients-ca.pem'],
          showWhen: { mode: 'listen', 'tls/enabled': true, 'tls/requireClientCert': true },
          requiredWhen: { mode: 'listen', 'tls/enabled': true, 'tls/requireClientCert': true },
        },
        serverCa: {
          title: 'Server CA (PEM)',
          description: "Signs the remote's certificate; empty trusts the system roots.",
          examples: ['file:/etc/sentinel/secrets/ca.pem'],
          showWhen: { mode: 'connect', 'tls/enabled': true },
        },
      },
    };
    const secretFields = ['tlsCert', 'tlsKey', 'clientCa', 'serverCa'];
    const cert = 'file:/etc/sentinel/secrets/server.pem';
    const key = 'file:/etc/sentinel/secrets/server.key';

    function secret(name: string): HTMLInputElement | null {
      return el.querySelector<HTMLInputElement>(`.sf-section-secrets input[name="${name}"]`);
    }

    function shownSecrets(): string[] {
      return secretFields.filter((name) => secret(name) !== null);
    }

    /** The label of a secret, asterisk included. */
    function labelOf(name: string): string | undefined {
      return el.querySelector(`.sf-section-secrets label[for="sf-secret-${name}"]`)?.textContent?.trim();
    }

    function set(pointer: string, value: unknown): void {
      component.control(pointer)?.setValue(value);
      fixture.detectChanges();
    }

    it('labels secrets from x-secrets and shows each only while its conditions hold', () => {
      render(tlsSchema, { secretFields });

      // TLS is off, so there is nothing to ask for, and the section stays out of the way
      expect(shownSecrets()).toEqual([]);
      expect(el.querySelector('.sf-section-secrets')).toBeNull();

      set('/config/tls/enabled', true);
      expect(shownSecrets()).toEqual(['tlsCert', 'tlsKey']);
      expect(labelOf('tlsCert')).toContain('TLS certificate (PEM)');
      expect(secret('tlsCert')?.getAttribute('placeholder')).toBe('file:/etc/sentinel/secrets/server.pem');
      expect(labelOf('tlsKey')).toContain('TLS private key (PEM)');
      expect(secret('tlsKey')?.getAttribute('placeholder')).toBe('file:/etc/sentinel/secrets/server.key');
      expect(el.querySelector('[data-pointer="/secretRefs/tlsCert"] .sf-help')?.textContent).toContain(
        'the certificate Sentinel presents',
      );

      // the client CA needs listening, TLS and client certificates, all three
      set('/config/tls/requireClientCert', true);
      expect(shownSecrets()).toEqual(['tlsCert', 'tlsKey', 'clientCa']);
      expect(labelOf('clientCa')).toContain('Client CA (PEM)');
      expect(secret('clientCa')?.getAttribute('placeholder')).toBe('file:/etc/sentinel/secrets/clients-ca.pem');

      set('/config/tls/enabled', false);
      expect(shownSecrets()).toEqual([]);

      set('/config/tls/enabled', true);
      set('/config/mode', 'connect');
      expect(shownSecrets()).toEqual(['tlsCert', 'tlsKey', 'serverCa']);
      expect(labelOf('serverCa')).toContain('Server CA (PEM)');
      expect(secret('serverCa')?.getAttribute('placeholder')).toBe('file:/etc/sentinel/secrets/ca.pem');
    });

    it('leaves hidden secrets out of secretRefs and validity', () => {
      render(tlsSchema, {
        secretFields,
        value: { tls: { enabled: true } },
        secretRefs: { tlsCert: cert, tlsKey: key, clientCa: 'not a reference', serverCa: 'file:/etc/sentinel/secrets/ca.pem' },
      });

      // a listener with TLS shows its certificate and key; the bogus client CA and the server CA stay hidden
      expect(shownSecrets()).toEqual(['tlsCert', 'tlsKey']);
      expect(last().valid).toBe(true);
      expect(last().secretRefs).toEqual({ tlsCert: cert, tlsKey: key });

      // asking for client certificates shows the client CA, which now counts
      set('/config/tls/requireClientCert', true);
      expect(last().valid).toBe(false);
      expect(last().secretRefs).toEqual({ tlsCert: cert, tlsKey: key, clientCa: 'not a reference' });

      // and it stops counting when it is hidden again
      set('/config/tls/requireClientCert', false);
      expect(last().valid).toBe(true);
      expect(last().secretRefs).toEqual({ tlsCert: cert, tlsKey: key });

      // what a hidden secret holds comes back with it
      set('/config/mode', 'connect');
      expect(last().secretRefs).toEqual({ tlsCert: cert, tlsKey: key, serverCa: 'file:/etc/sentinel/secrets/ca.pem' });
    });

    it('requires a secret while its requiredWhen holds', () => {
      render(tlsSchema, { secretFields, value: { tls: { enabled: true } } });

      // a listener presents a certificate, so it needs one and its key
      expect(labelOf('tlsCert')).toContain('*');
      expect(labelOf('tlsKey')).toContain('*');
      expect(last().valid).toBe(false);

      component.markAllTouched();
      fixture.detectChanges();
      const messages = (name: string) =>
        Array.from(el.querySelectorAll(`[data-pointer="/secretRefs/${name}"] > .sf-error`)).map((n) => n.textContent?.trim());
      expect(messages('tlsCert')).toEqual(['Required.']);
      expect(messages('tlsKey')).toEqual(['Required.']);

      setInput('input[name="tlsCert"]', cert);
      expect(last().valid).toBe(false);
      setInput('input[name="tlsKey"]', key);
      expect(last().valid).toBe(true);
      expect(messages('tlsCert')).toEqual([]);

      // mutual TLS adds the client CA to what it needs
      set('/config/tls/requireClientCert', true);
      expect(labelOf('clientCa')).toContain('*');
      expect(last().valid).toBe(false);
      setInput('input[name="clientCa"]', 'file:/etc/sentinel/secrets/clients-ca.pem');
      expect(last().valid).toBe(true);

      // connecting, the certificate is only an option, and so is the server CA
      set('/config/mode', 'connect');
      expect(shownSecrets()).toEqual(['tlsCert', 'tlsKey', 'serverCa']);
      expect(labelOf('tlsCert')).not.toContain('*');
      expect(labelOf('serverCa')).not.toContain('*');
      setInput('input[name="tlsCert"]', '');
      setInput('input[name="tlsKey"]', '');
      expect(last().valid).toBe(true);
    });

    it('keeps a secret that x-secrets does not list as it was: always shown, never required', () => {
      render(tlsSchema, { secretFields: ['password'] });

      expect(secret('password')).not.toBeNull();
      expect(labelOf('password')).toBe('Password');
      expect(secret('password')?.getAttribute('placeholder')).toBe('env:PASSWORD');
      expect(last().valid).toBe(true);
    });
  });

  describe('required defaults', () => {
    const modeSchema: JsonSchema = {
      type: 'object',
      required: ['mode'],
      properties: {
        mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen', title: 'Mode' },
      },
    };

    it('starts a required enum at its default when the value leaves it out', () => {
      render(modeSchema);

      expect(component.control('/config/mode')?.value).toBe('listen');
      expect(last().config['mode']).toBe('listen');
      expect(last().valid).toBe(true);

      const select = el.querySelector<HTMLSelectElement>('select[name="mode"]')!;
      expect(select.options[select.selectedIndex].textContent?.trim()).toBe('listen');
      // the empty option is only a prompt now: it cannot be picked back
      expect(select.options[0].disabled).toBe(true);
      expect(select.options[0].textContent?.trim()).toBe('Select…');
    });

    it('keeps a stored value over the default', () => {
      render(modeSchema, { value: { mode: 'connect' } });

      expect(component.control('/config/mode')?.value).toBe('connect');
      expect(last().config['mode']).toBe('connect');
    });

    it('does not swap a stored value the enum no longer offers', () => {
      render(modeSchema, { value: { mode: 'bogus' } });

      expect(component.control('/config/mode')?.value).toBe('');
      expect(last().valid).toBe(false);

      component.markAllTouched();
      fixture.detectChanges();
      expect(el.querySelector('[data-pointer="/config/mode"] .sf-error')?.textContent).toContain('Required.');
    });

    it('shows a required select with nothing chosen as a prompt that cannot be picked back', () => {
      render({
        type: 'object',
        required: ['kind'],
        properties: { kind: { type: 'string', enum: ['tcpclient', 'udp'], title: 'Kind' } },
      });

      const select = el.querySelector<HTMLSelectElement>('select[name="kind"]')!;
      expect(component.control('/config/kind')?.value).toBe('');
      expect(select.selectedIndex).toBe(0);
      expect(select.options[0].disabled).toBe(true);
      expect(select.options[0].textContent?.trim()).toBe('Select…');
      expect(last().valid).toBe(false);
    });

    it('keeps the empty option of an optional select enabled, with or without a default', () => {
      render({
        type: 'object',
        properties: {
          scope: { type: 'string', enum: ['mil', 'area'], default: 'mil' },
          kind: { type: 'string', enum: ['tcpclient', 'udp'] },
        },
      });

      for (const [name, empty] of [
        ['scope', 'Default (mil)'],
        ['kind', '—'],
      ]) {
        const select = el.querySelector<HTMLSelectElement>(`select[name="${name}"]`)!;
        expect(select.selectedIndex).toBe(0);
        expect(select.options[0].disabled).toBe(false);
        expect(select.options[0].textContent?.trim()).toBe(empty);
      }
      expect(last().valid).toBe(true);
    });

    it('starts any required field the value leaves out at its default', () => {
      render({
        type: 'object',
        required: ['retries', 'label', 'tags'],
        properties: {
          retries: { type: 'integer', default: 5 },
          label: { type: 'string', default: 'radar' },
          tags: { type: 'array', items: { type: 'string' }, default: ['a', 'b'] },
          // optional: the default stays a placeholder, for the server to apply
          spare: { type: 'integer', default: 7 },
        },
      });

      expect(component.control('/config/retries')?.value).toBe(5);
      expect(component.control('/config/label')?.value).toBe('radar');
      expect(component.control('/config/tags')?.value).toBe('a, b');
      expect(component.control('/config/spare')?.value).toBeNull();
      expect(last().config).toEqual({ retries: 5, label: 'radar', tags: ['a', 'b'] });
      expect(last().valid).toBe(true);
    });
  });

  describe('free-form objects', () => {
    const freeFormSchema: JsonSchema = {
      type: 'object',
      properties: {
        decoderOptions: {
          type: 'object',
          title: 'Decoder options',
          description: 'The field map.',
          examples: [{ fields: { latitude: 'lat', longitude: 'lon' } }],
        },
      },
    };
    const fieldMap = { records: 'ac', fields: { latitude: 'lat', longitude: 'lon' } };

    // Describes some of what a listener stores: `host` only in connect mode, `transport` only in part.
    const keptSchema: JsonSchema = {
      type: 'object',
      properties: {
        mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen' },
        host: { type: 'string', 'x-showWhen': { mode: 'connect' } },
        transport: {
          type: 'object',
          properties: {
            kind: { type: 'string', enum: ['tcpserver', 'udp'] },
            address: { type: 'string' },
            tls: { type: 'object', properties: { enabled: { type: 'boolean' } } },
          },
        },
      },
    };

    // Like httpjson: a POST body is an object, or a string that the driver sends as it is.
    const postSchema: JsonSchema = {
      type: 'object',
      properties: {
        url: { type: 'string', title: 'Request URL' },
        body: { type: ['object', 'string'], title: 'POST body' },
      },
    };

    function textarea(name: string): HTMLTextAreaElement {
      const found = el.querySelector<HTMLTextAreaElement>(`textarea[name="${name}"]`);
      if (!found) {
        throw new Error(`no textarea for ${name}`);
      }
      return found;
    }

    it('edits an object without properties as JSON and keeps the stored value', () => {
      render(freeFormSchema, { value: { decoderOptions: fieldMap } });

      expect(JSON.parse(textarea('decoderOptions').value)).toEqual(fieldMap);
      expect(last().config['decoderOptions']).toEqual(fieldMap);
      expect(el.querySelector('fieldset')).toBeNull();
    });

    it('offers the schema example as the placeholder and says the field takes JSON', () => {
      render(freeFormSchema);

      expect(textarea('decoderOptions').value).toBe('');
      expect(JSON.parse(textarea('decoderOptions').placeholder)).toEqual({ fields: { latitude: 'lat', longitude: 'lon' } });
      expect(el.querySelector('[data-pointer="/config/decoderOptions"] .sf-help')?.textContent).toBe('The field map. JSON object.');

      render({ type: 'object', properties: { headers: { type: 'object', additionalProperties: { type: 'string' } } } });
      expect(textarea('headers').placeholder).toBe('{ "key": "value" }');
      expect(el.querySelector('[data-pointer="/config/headers"] .sf-help')?.textContent).toBe('JSON object.');
    });

    it('refuses text that is not a JSON object, and leaves an empty one out', () => {
      render(freeFormSchema, { value: { decoderOptions: fieldMap } });
      const messages = () => el.querySelector('[data-pointer="/config/decoderOptions"] .sf-error')?.textContent;

      setInput('textarea[name="decoderOptions"]', '[1]');
      expect(component.control('/config/decoderOptions')?.hasError('jsonObject')).toBe(true);
      expect(messages()).toContain('Must be a JSON object');
      expect(last().valid).toBe(false);

      setInput('textarea[name="decoderOptions"]', '{"a": 1}');
      expect(last().valid).toBe(true);
      expect(last().config['decoderOptions']).toEqual({ a: 1 });

      setInput('textarea[name="decoderOptions"]', '{"a":');
      expect(component.control('/config/decoderOptions')?.hasError('json')).toBe(true);
      expect(messages()).toContain('Not valid JSON.');
      expect(last().valid).toBe(false);

      for (const empty of ['', '   ', '{}']) {
        setInput('textarea[name="decoderOptions"]', empty);
        expect(last().valid).toBe(true);
        expect('decoderOptions' in last().config).toBe(false);
      }
    });

    it('asks for the object when it is required', () => {
      render({ ...freeFormSchema, required: ['decoderOptions'] });
      expect(component.control('/config/decoderOptions')?.hasError('required')).toBe(true);
      expect(last().valid).toBe(false);

      component.markAllTouched();
      fixture.detectChanges();
      expect(el.querySelector('[data-pointer="/config/decoderOptions"] .sf-error')?.textContent).toContain('Required.');

      setInput('textarea[name="decoderOptions"]', '{"a": 1}');
      expect(last().valid).toBe(true);
    });

    it('starts a required object at its schema default, and keeps a stored one over it', () => {
      const defaulted: JsonSchema = {
        type: 'object',
        required: ['headers'],
        properties: { headers: { type: 'object', default: { Accept: 'application/json' } } },
      };
      render(defaulted);
      expect(JSON.parse(textarea('headers').value)).toEqual({ Accept: 'application/json' });
      expect(last().config['headers']).toEqual({ Accept: 'application/json' });
      expect(last().valid).toBe(true);

      render(defaulted, { value: { headers: { 'X-Key': 'abc' } } });
      expect(last().config['headers']).toEqual({ 'X-Key': 'abc' });
    });

    it('accepts a plain string where the schema allows object or string', () => {
      const bodySchema: JsonSchema = { type: 'object', properties: { body: { type: ['object', 'string'], title: 'POST body' } } };
      render(bodySchema, { value: { body: { q: 1 } } });

      expect(JSON.parse(textarea('body').value)).toEqual({ q: 1 });
      expect(last().config['body']).toEqual({ q: 1 });

      setInput('textarea[name="body"]', '  raw\n');
      expect(component.control('/config/body')?.valid).toBe(true);
      expect(last().valid).toBe(true);
      expect(last().config['body']).toBe('raw');

      setInput('textarea[name="body"]', '  ');
      expect(last().valid).toBe(true);
      expect('body' in last().config).toBe(false);

      setInput('textarea[name="body"]', '{"q": 2');
      expect(last().valid).toBe(true);
      expect(last().config['body']).toBe('{"q": 2');

      render(bodySchema, { value: { body: 'q={lat}' } });
      expect(textarea('body').value).toBe('q={lat}');
      expect(last().config['body']).toBe('q={lat}');
    });

    // The driver compacts an object body but sends a string as it is, so a string the form turns into
    // an object (or trims) changes what is sent, and a number parsed from it can lose digits.
    it('keeps a stored string body verbatim through an edit of another field', () => {
      render(postSchema, { value: { url: 'https://a.example', body: ' {"q":1} ' } });
      expect(textarea('body').value).toBe(' {"q":1} ');
      expect(last().config['body']).toBe(' {"q":1} ');

      setInput('input[name="url"]', 'https://b.example');
      expect(last().config['url']).toBe('https://b.example');
      expect(last().config['body']).toBe(' {"q":1} ');
    });

    it('keeps a stored "{}" string body', () => {
      render(postSchema, { value: { url: 'https://a.example', body: '{}' } });
      expect(last().config['body']).toBe('{}');

      setInput('input[name="url"]', 'https://b.example');
      expect(last().config['body']).toBe('{}');
    });

    it('keeps every digit of a stored string body', () => {
      const body = '{"id":9007199254740993}'; // 2^53 + 1, which a parsed number rounds
      render(postSchema, { value: { url: 'https://a.example', body } });

      setInput('input[name="url"]', 'https://b.example');
      expect(last().config['body']).toBe(body);
    });

    it('leaves out a stored string body that is blank, like any empty value', () => {
      render(postSchema, { value: { url: 'https://a.example', body: ' \n ' } });
      expect('body' in last().config).toBe(false);
    });

    it('follows the usual rules once the user types over a stored string body', () => {
      render(postSchema, { value: { body: ' {"q":1} ' } });

      setInput('textarea[name="body"]', ' {"q":2} ');
      expect(last().config['body']).toEqual({ q: 2 });

      setInput('textarea[name="body"]', '  q={lat}\n');
      expect(last().config['body']).toBe('q={lat}');

      setInput('textarea[name="body"]', '');
      expect('body' in last().config).toBe(false);
    });

    it('carries keys the schema does not describe through unchanged', () => {
      fixture.componentRef.setInput('externalKeys', ['classification']);
      render(keptSchema, {
        secretFields: ['clientSecret'],
        value: {
          mode: 'listen',
          host: 'stale.example',
          transport: { kind: 'tcpserver', address: ':4001', allow: ['10.0.0.0/8'], tls: { enabled: true, legacyCa: 'x' } },
          legacy: { keep: true },
          classification: 'SECRET',
          clientSecret: 'plain-text',
          staleWarnSec: 120,
        },
      });

      // allow and legacyCa sit in groups, legacy at the top; host is hidden, classification is the
      // parent's and clientSecret a secret, so those three stay out
      expect(last().config).toEqual({
        mode: 'listen',
        transport: { kind: 'tcpserver', address: ':4001', allow: ['10.0.0.0/8'], tls: { enabled: true, legacyCa: 'x' } },
        legacy: { keep: true },
        staleWarnSec: 120,
      });

      // a common option is a field of its own, so clearing it takes it out
      component.control('/config/staleWarnSec')?.setValue(null);
      fixture.detectChanges();
      expect('staleWarnSec' in last().config).toBe(false);
    });

    it('keeps undescribed keys and still drops hidden known keys', () => {
      render(
        {
          type: 'object',
          required: ['mode'],
          properties: {
            mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen' },
            host: { type: 'string', 'x-showWhen': { mode: 'connect' } },
            port: { type: 'integer', minimum: 1, maximum: 65535 },
            allow: { type: 'array', items: { type: 'string' }, 'x-showWhen': { mode: 'listen' } },
          },
        },
        { value: { mode: 'connect', host: 'h', port: 7000, allow: ['10.0.0.0/8'], legacyKey: 1 } },
      );

      expect(last().config).toEqual({ mode: 'connect', host: 'h', port: 7000, legacyKey: 1 });

      // the hidden key is still known to the form: listening brings it back, and legacyKey stays
      component.control('/config/mode')?.setValue('listen');
      fixture.detectChanges();
      expect(last().config).toEqual({ mode: 'listen', port: 7000, allow: ['10.0.0.0/8'], legacyKey: 1 });
    });

    it('drops the extras of a hidden group along with it', () => {
      render(
        {
          type: 'object',
          properties: {
            format: { type: 'string', enum: ['sentinel', 'json'], default: 'sentinel' },
            decoderOptions: {
              type: 'object',
              properties: { timeField: { type: 'string' } },
              'x-showWhen': { format: 'json' },
            },
          },
        },
        { value: { decoderOptions: { timeField: 'ts', legacy: 1 } } },
      );
      expect(last().config['decoderOptions']).toBeUndefined();

      component.control('/config/format')?.setValue('json');
      fixture.detectChanges();
      expect(last().config['decoderOptions']).toEqual({ timeField: 'ts', legacy: 1 });
    });

    it('names the kept keys', () => {
      render(keptSchema, { value: { transport: { kind: 'tcpserver', allow: ['10.0.0.0/8'], keepalive: 30 }, legacy: 1 } });

      const group = el.querySelector('[data-pointer="/config/transport"] > .sf-extras');
      expect(group?.textContent).toContain('Kept as stored');
      expect(group?.textContent).toMatch(/allow, keepalive/);
      expect(group?.textContent).not.toContain('legacy');

      const top = el.querySelector('.sf-section-driver > .sf-extras');
      expect(top?.textContent).toContain('legacy');
      expect(top?.textContent).not.toContain('allow');

      render(keptSchema, { value: { transport: { kind: 'udp' } } });
      expect(el.querySelector('.sf-extras')).toBeNull();
    });

    describe('where the schema forbids extra keys', () => {
      it('carries none at the root, and names none', () => {
        const open: JsonSchema = { type: 'object', properties: { port: { type: 'integer' } } };
        const value = { port: 7000, legacy: 1 };

        render(open, { value });
        expect(last().config).toEqual({ port: 7000, legacy: 1 });

        // only `false` closes the schema: extra keys that must match a schema are allowed
        render({ ...open, additionalProperties: { type: 'integer' } }, { value });
        expect(last().config).toEqual({ port: 7000, legacy: 1 });

        render({ ...open, additionalProperties: false }, { value });
        expect(last().config).toEqual({ port: 7000 });
        expect(el.querySelector('.sf-extras')).toBeNull();
      });

      it('carries none into a group that forbids them, but still does into one that allows them', () => {
        render(
          {
            type: 'object',
            properties: {
              tls: { type: 'object', additionalProperties: false, properties: { enabled: { type: 'boolean' } } },
              options: { type: 'object', additionalProperties: true, properties: { name: { type: 'string' } } },
            },
          },
          { value: { tls: { enabled: true, certFile: '/x' }, options: { name: 'n', legacy: 2 } } },
        );

        expect(last().config).toEqual({ tls: { enabled: true }, options: { name: 'n', legacy: 2 } });
        expect(el.querySelector('[data-pointer="/config/tls"]')).not.toBeNull();
        expect(el.querySelector('[data-pointer="/config/tls"] .sf-extras')).toBeNull();
        expect(el.querySelector('[data-pointer="/config/options"] .sf-extras')?.textContent).toContain('legacy');
      });

      it('carries none into the delivery tuning group, which COMMON_OPTIONS_SCHEMA closes', () => {
        render(
          { type: 'object', properties: { port: { type: 'integer' } } },
          { value: { port: 7000, outbound: { queueMax: 500, retired: 1 } } },
        );

        expect(el.querySelector('[data-pointer="/config/outbound"]')).not.toBeNull();
        expect(last().config['outbound']).toEqual({ queueMax: 500 });
        expect(el.querySelector('[data-pointer="/config/outbound"] .sf-extras')).toBeNull();
      });
    });

    it('maps a server problem inside the object to the JSON field', () => {
      render(freeFormSchema, { value: { decoderOptions: { fields: {} } } });

      fixture.componentRef.setInput('problems', [
        { path: '/config/decoderOptions/fields/latitude', message: 'is required' },
      ]);
      fixture.detectChanges();

      const shown = Array.from(el.querySelectorAll('[data-pointer="/config/decoderOptions"] .sf-error-server')).map((n) =>
        n.textContent?.trim(),
      );
      expect(shown).toEqual(['fields/latitude: is required']);
      expect(el.querySelector('.sf-unmatched')).toBeNull();
    });

    // The server reports what is absent or wrong at that property's own pointer. A JSON field is one control
    // for everything inside it, and a group may have no field for the property, so the message has to say
    // which property it is about.
    describe('a server problem below a JSON field or a group', () => {
      it('names the property a problem inside a JSON field is about', () => {
        render(freeFormSchema, { value: { decoderOptions: { name: 7 } } });

        fixture.componentRef.setInput('problems', [
          { path: '/config/decoderOptions/sourceEntityId', message: 'is required' },
          { path: '/config/decoderOptions/name', message: 'got number, want string' },
          { path: '/config/decoderOptions/entityType', message: 'minLength: got 0, want 1' },
        ]);
        fixture.detectChanges();

        expect(serverMessages('/config/decoderOptions')).toEqual([
          'sourceEntityId: is required',
          'name: got number, want string',
          'entityType: minLength: got 0, want 1',
        ]);
        expect(el.querySelector('.sf-unmatched')).toBeNull();
      });

      it('names an escaped property by the key it stands for', () => {
        render(freeFormSchema);

        fixture.componentRef.setInput('problems', [
          { path: '/config/decoderOptions/a~1b', message: 'x' },
          { path: '/config/decoderOptions/c~0d', message: 'y' },
          // ~1 is read before ~0, so this is the key "e~1f" and not "e/f"
          { path: '/config/decoderOptions/e~01f', message: 'z' },
        ]);
        fixture.detectChanges();

        expect(serverMessages('/config/decoderOptions')).toEqual(['a/b: x', 'c~d: y', 'e~1f: z']);
      });

      it('shows a problem at the JSON field itself as the server wrote it', () => {
        render(freeFormSchema);

        fixture.componentRef.setInput('problems', [
          { path: '/config/decoderOptions', message: 'got string, want object' },
        ]);
        fixture.detectChanges();

        expect(serverMessages('/config/decoderOptions')).toEqual(['got string, want object']);
      });

      it('names the property for a problem below a group, and nothing for one at a field of the group', () => {
        render(keptSchema, { value: { transport: { kind: 'tcpserver', keepalive: 30 } } });

        fixture.componentRef.setInput('problems', [
          { path: '/config/transport/address', message: 'is required' },
          { path: '/config/transport/keepalive', message: 'got number, want string' },
          { path: '/config/transport/tls/legacyCa/pem', message: 'is required' },
          { path: '/config/transport/tls', message: "additional properties 'legacyCa' not allowed" },
        ]);
        fixture.detectChanges();

        expect(serverMessages('/config/transport/address')).toEqual(['is required']);
        expect(serverMessages('/config/transport')).toEqual(['keepalive: got number, want string']);
        expect(serverMessages('/config/transport/tls')).toEqual([
          'legacyCa/pem: is required',
          "additional properties 'legacyCa' not allowed",
        ]);
        expect(el.querySelector('.sf-unmatched')).toBeNull();
      });

      it('names a property the group requires but has no field for', () => {
        render({
          type: 'object',
          properties: {
            transport: {
              type: 'object',
              required: ['address'],
              properties: { kind: { type: 'string', enum: ['tcpserver', 'udp'] } },
            },
          },
        });

        fixture.componentRef.setInput('problems', [{ path: '/config/transport/address', message: 'is required' }]);
        fixture.detectChanges();

        expect(serverMessages('/config/transport')).toEqual(['address: is required']);
        expect(el.querySelector('.sf-unmatched')).toBeNull();
      });

      it('leaves the message of a problem inside a list as it is, the list being one control', () => {
        render({
          type: 'object',
          properties: {
            groups: { type: 'array', items: { type: 'string' } },
            transport: { type: 'object', properties: { allow: { type: 'array', items: { type: 'string' } } } },
          },
        });

        fixture.componentRef.setInput('problems', [
          { path: '/config/groups/1', message: 'must be non-empty' },
          { path: '/config/transport/allow/0', message: 'not a CIDR' },
        ]);
        fixture.detectChanges();

        expect(serverMessages('/config/groups')).toEqual(['must be non-empty']);
        expect(serverMessages('/config/transport/allow')).toEqual(['not a CIDR']);
        expect(serverMessages('/config/transport')).toEqual([]);
      });
    });
  });

  // Remarks the parent has about fields: a free port to take, a warning about an open listener. They sit with the
  // field's own messages and offer an action, but they are not errors and never decide whether the form is valid.
  describe('hints', () => {
    const hintSchema: JsonSchema = {
      type: 'object',
      required: ['mode'],
      properties: {
        mode: { type: 'string', enum: ['listen', 'connect'], default: 'listen' },
        host: { type: 'string', title: 'Host', 'x-showWhen': { mode: 'connect' } },
        port: { type: 'integer', minimum: 1024, title: 'Port' },
        label: { type: 'string', title: 'Label' },
        transport: {
          type: 'object',
          title: 'Transport',
          properties: { address: { type: 'string', title: 'Address' } },
        },
      },
    };

    function setHints(hints: FieldHint[]): void {
      fixture.componentRef.setInput('hints', hints);
      fixture.detectChanges();
    }

    /** The hints shown with the field or group at `pointer`, not with the fields inside it. */
    function hintsAt(pointer: string): HTMLElement[] {
      return Array.from(el.querySelectorAll<HTMLElement>(`[data-pointer="${pointer}"] > .sf-hint`));
    }

    function allHints(): Array<string | undefined> {
      return Array.from(el.querySelectorAll('.sf-hint')).map((n) => n.textContent?.replace(/\s+/g, ' ').trim());
    }

    /** What a note reads as: its text on one line. */
    function textOf(node: Element | null | undefined): string | undefined {
      return node?.textContent?.replace(/\s+/g, ' ').trim();
    }

    it('renders a hint under the field it names and applies its action', () => {
      render(hintSchema);
      setHints([{ path: '/config/port', message: 'Port 6001 is free.', action: { label: 'Use 6001', value: 6001 } }]);

      const [hint] = hintsAt('/config/port');
      expect(hint.textContent).toContain('Port 6001 is free.');
      expect(hint.classList.contains('sf-hint-warn')).toBe(false);
      // after the field's own control, with its other messages
      expect(el.querySelector('[data-pointer="/config/port"]')?.lastElementChild).toBe(hint);

      const use = hint.querySelector('button')!;
      expect(use.textContent?.trim()).toBe('Use 6001');
      // inside the form, a button must not submit it
      expect(use.type).toBe('button');

      const emitted = changes.length;
      use.click();
      fixture.detectChanges();

      expect(el.querySelector<HTMLInputElement>('input[name="port"]')?.value).toBe('6001');
      expect(component.control('/config/port')?.value).toBe(6001);
      expect(changes.length).toBeGreaterThan(emitted);
      expect(last().config['port']).toBe(6001);

      // it counts as an edit of the field
      expect(component.control('/config/port')?.dirty).toBe(true);
      expect(component.control('/config/port')?.touched).toBe(true);
    });

    it('marks a warning, and shows a remark with no action as plain text', () => {
      render(hintSchema);
      setHints([
        { path: '/config/port', level: 'warn', message: 'Port 80 is below the floor.' },
        { path: '/config/label', level: 'info', message: 'Just so you know.' },
      ]);

      const [warning] = hintsAt('/config/port');
      expect(warning.classList.contains('sf-hint-warn')).toBe(true);
      expect(warning.querySelector('button')).toBeNull();
      const [info] = hintsAt('/config/label');
      expect(info.classList.contains('sf-hint-warn')).toBe(false);
      expect(info.querySelector('button')).toBeNull();
    });

    it('gives a text field the value as it is, and a number field a number', () => {
      render(hintSchema);
      setHints([
        { path: '/config/label', message: 'Try this.', action: { label: 'Use north', value: 'north' } },
        { path: '/config/port', message: 'Try this.', action: { label: 'Use 6003', value: '6003' } },
      ]);

      hintsAt('/config/label')[0].querySelector('button')!.click();
      hintsAt('/config/port')[0].querySelector('button')!.click();
      fixture.detectChanges();

      expect(last().config['label']).toBe('north');
      expect(component.control('/config/port')?.value).toBe(6003);
      expect(last().config['port']).toBe(6003);
    });

    it('renders hints on a group and on the fields inside it, each on its own pointer', () => {
      render(hintSchema);
      setHints([
        { path: '/config/transport', level: 'warn', message: 'About the transport.' },
        { path: '/config/transport/address', message: 'About the address.', action: { label: 'Use :6001', value: ':6001' } },
      ]);

      expect(hintsAt('/config/transport').map((h) => h.textContent?.trim())).toEqual(['Warning: About the transport.']);
      expect(hintsAt('/config/transport/address').map((h) => h.textContent?.replace(/\s+/g, ' ').trim())).toEqual([
        'About the address. Use :6001',
      ]);
      expect(el.querySelector('fieldset[data-pointer="/config/transport"] > .sf-hint')).not.toBeNull();

      hintsAt('/config/transport/address')[0].querySelector('button')!.click();
      fixture.detectChanges();
      expect(el.querySelector<HTMLInputElement>('input[name="address"]')?.value).toBe(':6001');
      expect(last().config['transport']).toEqual({ address: ':6001' });
    });

    it('drops hints for hidden or unknown fields', () => {
      render(hintSchema);
      setHints([
        { path: '/config/host', message: 'About the host.' },
        { path: '/config/nope', message: 'About nothing.' },
        { path: '/config', message: 'About everything.' },
        { path: '/config/por', message: 'About a prefix of the port.' },
        { path: '/config/port/x', message: 'About below the port.' },
        { path: '/config/port', message: 'About the port.' },
      ]);

      // the host is hidden in listen mode, and nothing else is named by a pointer of the form
      expect(allHints()).toEqual(['About the port.']);

      // a field that comes into view brings its hint
      component.control('/config/mode')!.setValue('connect');
      fixture.detectChanges();
      expect(allHints()).toEqual(['About the host.', 'About the port.']);
    });

    it('follows the hints it is given', () => {
      render(hintSchema);
      setHints([{ path: '/config/port', message: 'First.' }]);
      expect(allHints()).toEqual(['First.']);

      // in the order of the fields, whatever the order of the hints
      setHints([{ path: '/config/label', message: 'Third.' }, { path: '/config/port', message: 'Second.' }]);
      expect(allHints()).toEqual(['Second.', 'Third.']);

      setHints([]);
      expect(allHints()).toEqual([]);
    });

    it('hints never change validity', () => {
      render(hintSchema, { value: { port: 80 } });
      expect(component.valid).toBe(false);
      const emitted = changes.length;

      setHints([
        { path: '/config/port', level: 'warn', message: 'Port 80 is below the floor.', action: { label: 'Use 6001', value: 6001 } },
        { path: '/config/label', level: 'warn', message: 'Careful.' },
      ]);

      // a warning is no error: the form is as valid as it was, nothing turns red, no message of the field's own appears
      expect(component.valid).toBe(false);
      expect(changes.length).toBe(emitted);
      expect(el.querySelector('[data-pointer="/config/label"]')?.classList.contains('has-issue')).toBe(false);
      expect(component.hasIssue(component.driverFields().find((d) => d.key === 'label')!)).toBe(false);
      expect(el.querySelectorAll('.sf-error').length).toBe(0);

      setInput('input[name="port"]', '6001');
      expect(component.valid).toBe(true);
      expect(last().valid).toBe(true);
      expect(allHints()).toHaveLength(2);
    });

    it('does nothing while the form is disabled', () => {
      render(hintSchema);
      setHints([{ path: '/config/port', message: 'Try this.', action: { label: 'Use 6001', value: 6001 } }]);
      fixture.componentRef.setInput('disabled', true);
      fixture.detectChanges();

      const use = hintsAt('/config/port')[0].querySelector('button')!;
      expect(use.disabled).toBe(true);
      use.click();
      // and not through the component either
      const port = component.driverFields().find((d) => d.key === 'port')!;
      component.applyHint(port, { path: '/config/port', message: 'Try this.', action: { label: 'Use 6001', value: 6001 } });

      expect(component.control('/config/port')?.value).toBeNull();
      expect(document.activeElement).not.toBe(el.querySelector('input[name="port"]'));
    });

    it('ignores an action whose value a number field cannot take', () => {
      render(hintSchema);
      const port = component.driverFields().find((d) => d.key === 'port')!;

      component.applyHint(port, { path: '/config/port', message: '', action: { label: 'Use it', value: 'six' } });
      component.applyHint(port, { path: '/config/port', message: 'No action here.' });

      expect(component.control('/config/port')?.value).toBeNull();
      expect(component.control('/config/port')?.dirty).toBe(false);
    });

    // A screen reader reads what a control names in aria-describedby along with the control, so a hint that is only
    // drawn beside a field is not heard by those who cannot see it.
    describe('for those who cannot see it', () => {
      const kindsSchema: JsonSchema = {
        type: 'object',
        properties: {
          label: { type: 'string', title: 'Label' },
          port: { type: 'integer', minimum: 1024, title: 'Port', description: 'The port to open.' },
          mode: { type: 'string', enum: ['listen', 'connect'] },
          enabled: { type: 'boolean', title: 'Enabled' },
          options: { type: 'object', title: 'Options' },
          fixed: { type: 'string', const: 'fixed', description: 'Never changes.' },
          transport: {
            type: 'object',
            title: 'Transport',
            description: 'Where it listens.',
            properties: { address: { type: 'string', title: 'Address' } },
          },
        },
      };

      it('names each hint in the aria-describedby of its control, whatever the kind of field', () => {
        render(kindsSchema, { secretFields: ['token'] });
        const controls: Record<string, string> = {
          '/config/label': 'input[name="label"]',
          '/config/port': 'input[name="port"]',
          '/config/mode': 'select[name="mode"]',
          '/config/enabled': 'input[name="enabled"]',
          '/config/options': 'textarea[name="options"]',
          '/secretRefs/token': 'input[name="token"]',
          '/config/transport': 'fieldset[data-pointer="/config/transport"]',
          '/config/transport/address': 'input[name="address"]',
        };
        setHints(Object.keys(controls).map((path) => ({ path, message: `About ${path}.` })));

        for (const [path, selector] of Object.entries(controls)) {
          const hint = hintsAt(path)[0];
          expect(hint.id).toMatch(/\S/);
          expect(el.querySelector(selector)?.getAttribute('aria-describedby')?.split(' ')).toContain(hint.id);
          // the id is the hint's alone
          expect(el.querySelectorAll(`[id="${hint.id}"]`)).toHaveLength(1);
        }
      });

      it('lists the help, errors, server problems and hints of a field in the order they are shown', () => {
        render(kindsSchema, { value: { port: 80 } });
        component.markAllTouched();
        fixture.componentRef.setInput('problems', [{ path: '/config/port', message: 'taken' }]);
        setHints([
          { path: '/config/port', level: 'warn', message: 'First hint.' },
          { path: '/config/port', message: 'Second hint.' },
        ]);

        const ids = el.querySelector('input[name="port"]')!.getAttribute('aria-describedby')!.split(' ');

        expect(ids.map((id) => textOf(el.querySelector(`[id="${id}"]`)))).toEqual([
          'The port to open.',
          'Must be at least 1024.',
          'taken',
          'Warning: First hint.',
          'Second hint.',
        ]);
        // the group and the constant have their help to be read with them
        expect(el.querySelector('fieldset[data-pointer="/config/transport"]')?.getAttribute('aria-describedby')).toBe(
          'sf-transport__help',
        );
        expect(el.querySelector('input[name="fixed"]')?.getAttribute('aria-describedby')).toBe('sf-fixed__help');
        expect(el.querySelector('#sf-fixed__help')?.textContent).toBe('Never changes.');
      });

      it('leaves aria-describedby off a field that nothing describes', () => {
        render(kindsSchema);
        const label = () => el.querySelector('input[name="label"]');
        expect(label()?.hasAttribute('aria-describedby')).toBe(false);

        setHints([{ path: '/config/label', message: 'A word.' }]);
        expect(label()?.hasAttribute('aria-describedby')).toBe(true);

        setHints([]);
        expect(label()?.hasAttribute('aria-describedby')).toBe(false);
      });

      it('says Warning in words for a warning, not by colour alone', () => {
        render(kindsSchema);
        setHints([
          { path: '/config/port', level: 'warn', message: 'Port 80 is below the floor.' },
          { path: '/config/label', level: 'info', message: 'Just so you know.' },
          { path: '/config/mode', message: 'Nothing said about the level.' },
        ]);

        expect(textOf(hintsAt('/config/port')[0])).toBe('Warning: Port 80 is below the floor.');
        expect(textOf(hintsAt('/config/label')[0])).toBe('Just so you know.');
        expect(textOf(hintsAt('/config/mode')[0])).toBe('Nothing said about the level.');
        // and it is read with the field
        const ids = el.querySelector('input[name="port"]')!.getAttribute('aria-describedby')!.split(' ');
        expect(textOf(el.querySelector(`[id="${ids[ids.length - 1]}"]`))).toContain('Warning: Port 80');
      });

      it('moves the focus to the field when an action is taken, so it does not fall to the page with the button', () => {
        render(kindsSchema);
        const hints: FieldHint[] = [
          { path: '/config/label', message: 'Try this.', action: { label: 'Use north', value: 'north' } },
          { path: '/config/port', message: 'Try this.', action: { label: 'Use 6003', value: 6003 } },
          { path: '/config/mode', message: 'Try this.', action: { label: 'Use listen', value: 'listen' } },
        ];

        for (const [path, selector] of [
          ['/config/label', 'input[name="label"]'],
          ['/config/port', 'input[name="port"]'],
          ['/config/mode', 'select[name="mode"]'],
        ]) {
          setHints(hints);
          const use = hintsAt(path)[0].querySelector('button')!;
          use.focus();
          expect(document.activeElement).toBe(use);

          use.click();
          // the parent takes the hint away once the field is set, and the button with it
          setHints([]);

          expect(document.activeElement).toBe(el.querySelector(selector));
        }
      });
    });
  });
});
