import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormGroup } from '@angular/forms';
import { SchemaFormChange, SchemaFormComponent } from './schema-form.component';
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
      { path: '/config/transport', message: "missing properties: 'address'" },
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
    expect(text('/config/transport')).toEqual(["missing properties: 'address'"]);
    expect(text('/config/groups')).toEqual(['must be non-empty']);
    expect(text('/secretRefs/clientSecret')).toEqual(['env var not set']);

    // advanced opened itself because one of its fields has a problem
    expect(component.advancedOpen()).toBe(true);
    expect(text('/config/staleWarnSec')).toEqual(['too low']);

    const unmatched = el.querySelector('.sf-unmatched');
    expect(unmatched?.textContent).toContain('/config/nope');
    expect(unmatched?.textContent).toContain('unknown property');
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

    it('maps a server problem inside the object to the JSON field', () => {
      render(freeFormSchema, { value: { decoderOptions: { fields: {} } } });

      fixture.componentRef.setInput('problems', [
        { path: '/config/decoderOptions/fields/latitude', message: 'latitude is required' },
      ]);
      fixture.detectChanges();

      const shown = Array.from(el.querySelectorAll('[data-pointer="/config/decoderOptions"] .sf-error-server')).map((n) =>
        n.textContent?.trim(),
      );
      expect(shown).toEqual(['latitude is required']);
      expect(el.querySelector('.sf-unmatched')).toBeNull();
    });
  });
});
