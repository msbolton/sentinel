import {
  ChangeDetectionStrategy,
  Component,
  computed,
  effect,
  input,
  output,
  signal,
  untracked,
} from '@angular/core';
import { NgTemplateOutlet } from '@angular/common';
import {
  AbstractControl,
  FormControl,
  FormGroup,
  ReactiveFormsModule,
  ValidationErrors,
  ValidatorFn,
  Validators,
} from '@angular/forms';
import { Subscription } from 'rxjs';
import { FeedProblem, JsonSchema, JsonType, SecretMeta } from '../../models/feed.model';
import { COMMON_OPTION_KEYS, COMMON_OPTIONS_SCHEMA } from './common-options';

/** What the form emits whenever anything changes. */
export interface SchemaFormChange {
  config: Record<string, unknown>;
  secretRefs: Record<string, string>;
  valid: boolean;
}

export type FieldKind = 'text' | 'url' | 'number' | 'boolean' | 'select' | 'const' | 'array' | 'group' | 'secret' | 'json';

/** The value at `path` (read from a group of the form) is one of `values`. */
export interface Condition {
  path: string[];
  values: unknown[];
}

/** What a schema asks of a field, but only while every `when` condition holds. */
export interface Rule {
  when: Condition[];
  required?: boolean;
  minimum?: number;
  maximum?: number;
}

/** One rendered field; groups carry their children. */
export interface FieldDef {
  /** Property name within its parent object. */
  key: string;
  /** DOM id, unique within the form. */
  id: string;
  /** JSON pointer the server would report problems under. */
  pointer: string;
  kind: FieldKind;
  label: string;
  help?: string;
  placeholder?: string;
  required: boolean;
  step?: string;
  options?: Array<string | number>;
  /** `default` of an enum, named in the empty option. */
  defaultLabel?: string;
  constValue?: unknown;
  /** `json`: the schema takes a plain string as well as an object. */
  allowString?: boolean;
  /** `json` with `allowString`: the string the field started with, emitted as written while the text is unchanged. */
  stored?: string;
  control?: FormControl;
  children?: FieldDef[];
  /** `group`: stored keys the schema does not describe, kept as they are. */
  extras?: Record<string, unknown>;
  /** `x-showWhen`: shown only while every condition holds. */
  showWhen?: Condition[];
  /** Requirements that bind only while their conditions hold: `allOf` rules, a secret's `requiredWhen`. */
  rules?: Rule[];
  /**
   * The group the conditions of `showWhen` and `rules` are read from: the one this field's control lives in
   * (its siblings), or the config root for a secret, whose control lives apart.
   */
  parent?: FormGroup;
}

export const SECRET_REF_PATTERN = /^(env:[A-Z0-9_]+|file:\/\S+)$/;

/**
 * Builds a reactive form from a driver's JSON Schema. Driver options and
 * the common runtime options both land in `config`; secret references
 * land in `secretRefs`. Empty optional values are left out of the
 * emitted config so the server applies its defaults.
 */
@Component({
  selector: 'app-schema-form',
  standalone: true,
  imports: [ReactiveFormsModule, NgTemplateOutlet],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './schema-form.component.html',
  styleUrl: './schema-form.component.scss',
})
export class SchemaFormComponent {
  readonly schema = input<JsonSchema | null | undefined>(undefined);
  readonly secretFields = input<string[]>([]);
  readonly value = input<Record<string, unknown>>({});
  readonly secretRefs = input<Record<string, string>>({});
  readonly disabled = input(false);
  readonly problems = input<FeedProblem[]>([]);
  /**
   * Config keys the parent owns (classification, policy…): no control is
   * built for them and they never appear in the emitted config, whatever
   * the schema or the initial value says.
   */
  readonly externalKeys = input<string[]>([]);

  readonly changed = output<SchemaFormChange>();

  readonly driverFields = signal<FieldDef[]>([]);
  readonly advancedFields = signal<FieldDef[]>([]);
  readonly secretDefs = signal<FieldDef[]>([]);
  /** Stored top-level keys the form has no field for, kept as they are. */
  readonly extras = signal<Record<string, unknown>>({});
  readonly advancedOpen = signal(false);
  /** Bumped on every form change so OnPush templates re-read control state. */
  readonly tick = signal(0);
  /** Set by the parent (via markAllTouched) to surface every error at once. */
  readonly showAll = signal(false);

  readonly configGroup = new FormGroup<Record<string, AbstractControl>>({});
  readonly secretGroup = new FormGroup<Record<string, AbstractControl>>({});

  private readonly problemIndex = computed(() => {
    const index = new Map<string, string[]>();
    for (const p of this.problems()) {
      const list = index.get(p.path) ?? [];
      list.push(p.message);
      index.set(p.path, list);
    }
    return index;
  });

  /** Problems resolved to the field (or group) they belong to, else to the form. */
  private readonly resolvedProblems = computed(() => {
    const pointers = this.allPointers();
    const byPointer = new Map<string, string[]>();
    const unmatched: FeedProblem[] = [];
    for (const problem of this.problems()) {
      const target = this.resolvePointer(problem.path, pointers);
      if (target === null) {
        unmatched.push(problem);
        continue;
      }
      const list = byPointer.get(target) ?? [];
      list.push(problem.message);
      byPointer.set(target, list);
    }
    return { byPointer, unmatched };
  });

  readonly unmatchedProblems = computed(() => this.resolvedProblems().unmatched);

  private readonly allPointers = computed(() => {
    const out: string[] = [];
    const walk = (defs: FieldDef[]) => {
      for (const def of defs) {
        out.push(def.pointer);
        if (def.children) {
          walk(def.children);
        }
      }
    };
    walk(this.driverFields());
    walk(this.advancedFields());
    walk(this.secretDefs());
    return out;
  });

  /** Property schemas of each group built, so a `showWhen` sibling's default can be found. */
  private readonly siblingSchemas = new WeakMap<FormGroup, Record<string, JsonSchema>>();

  private changeSub: Subscription | null = null;

  constructor() {
    effect(() => {
      const schema = this.schema();
      const secretFields = this.secretFields();
      const value = this.value();
      const secretRefs = this.secretRefs();
      const externalKeys = this.externalKeys();
      untracked(() => this.build(schema ?? {}, secretFields, value ?? {}, secretRefs ?? {}, externalKeys ?? []));
    });
    effect(() => {
      const disabled = this.disabled();
      untracked(() => {
        if (disabled) {
          this.configGroup.disable({ emitEvent: false });
          this.secretGroup.disable({ emitEvent: false });
        } else {
          this.configGroup.enable({ emitEvent: false });
          this.secretGroup.enable({ emitEvent: false });
        }
        this.tick.update((n) => n + 1);
      });
    });
    effect(() => {
      // Problems under the advanced section should be visible.
      const index = this.problemIndex();
      const advanced = this.advancedFields();
      if (advanced.some((d) => index.has(d.pointer))) {
        untracked(() => this.advancedOpen.set(true));
      }
    });
  }

  /** Hidden fields keep their controls but do not count: only the fields on show must be valid. */
  get valid(): boolean {
    return this.allValid([...this.driverFields(), ...this.advancedFields(), ...this.secretDefs()]);
  }

  /** Current snapshot, the same shape `changed` emits. */
  snapshot(): SchemaFormChange {
    return {
      config: { ...this.extras(), ...this.collect([...this.driverFields(), ...this.advancedFields()]) },
      secretRefs: this.collectSecrets(),
      valid: this.valid,
    };
  }

  /** Surfaces every validation error, e.g. when the parent's submit is refused. */
  markAllTouched(): void {
    this.configGroup.markAllAsTouched();
    this.secretGroup.markAllAsTouched();
    this.showAll.set(true);
    this.advancedOpen.set(this.advancedOpen() || !this.allValid(this.advancedFields()));
    this.tick.update((n) => n + 1);
  }

  /** Looks a control up by its JSON pointer (`/config/transport/kind`, `/secretRefs/password`). */
  control(pointer: string): AbstractControl | null {
    if (pointer.startsWith('/secretRefs/')) {
      return this.secretGroup.get(pointer.slice('/secretRefs/'.length)) ?? null;
    }
    if (pointer.startsWith('/config/')) {
      return this.configGroup.get(pointer.slice('/config/'.length).split('/')) ?? null;
    }
    return null;
  }

  toggleAdvanced(): void {
    this.advancedOpen.update((open) => !open);
  }

  touch(): void {
    this.tick.update((n) => n + 1);
  }

  /** Client-side validation messages for a field, once it has been touched. */
  errorsOf(def: FieldDef): string[] {
    this.tick();
    const control = def.control;
    if (!(control?.touched || control?.dirty || this.showAll())) {
      return [];
    }
    // a rule's minimum replaces the field's own, so a value out of both ranges gets one message
    return describeErrors({ ...control?.errors, ...this.conditionalErrors(def) });
  }

  /** Server-side messages addressed to this field (or group). */
  problemsOf(def: FieldDef): string[] {
    return this.resolvedProblems().byPointer.get(def.pointer) ?? [];
  }

  hasIssue(def: FieldDef): boolean {
    return this.errorsOf(def).length > 0 || this.problemsOf(def).length > 0;
  }

  /** The names of the stored keys a form or group keeps without showing them. */
  extraKeys(extras: Record<string, unknown> | undefined): string[] {
    return Object.keys(extras ?? {});
  }

  /** A field with `x-showWhen` is shown only while each of its conditions holds. */
  isShown(def: FieldDef, siblings: FormGroup | undefined = def.parent): boolean {
    this.tick(); // re-evaluate on every form change (OnPush)
    return this.visible(def, siblings);
  }

  /** Whether a field is marked as required: outright, or by a rule whose conditions hold right now. */
  isRequired(def: FieldDef): boolean {
    this.tick(); // re-evaluate on every form change (OnPush)
    return def.required || this.activeRules(def).some((rule) => rule.required);
  }

  /** Whether any secret is on show; while none is, the Credentials section has nothing to ask. */
  hasVisibleSecrets(): boolean {
    this.tick(); // re-evaluate on every form change (OnPush)
    return this.secretDefs().some((def) => this.visible(def));
  }

  // ---------------------------------------------------------------------
  // Building
  // ---------------------------------------------------------------------

  private build(
    schema: JsonSchema,
    secretFields: string[],
    value: Record<string, unknown>,
    secretRefs: Record<string, string>,
    externalKeys: string[] = [],
  ): void {
    this.changeSub?.unsubscribe();
    for (const key of Object.keys(this.configGroup.controls)) {
      this.configGroup.removeControl(key, { emitEvent: false });
    }
    for (const key of Object.keys(this.secretGroup.controls)) {
      this.secretGroup.removeControl(key, { emitEvent: false });
    }
    this.showAll.set(false);

    const external = new Set(externalKeys);
    const properties = schema.properties ?? {};
    const required = new Set(schema.required ?? []);
    const secretKeys = new Set<string>(secretFields);
    for (const [key, prop] of Object.entries(properties)) {
      if (prop['x-secret']) {
        secretKeys.add(key);
      }
    }

    this.siblingSchemas.set(this.configGroup, { ...(COMMON_OPTIONS_SCHEMA.properties ?? {}), ...properties });

    // What the schema, the common options, the parent and the secrets do not claim is kept as stored,
    // unless the schema forbids keys of its own: the server would refuse them.
    const claimed = new Set([...Object.keys(properties), ...COMMON_OPTION_KEYS, ...external, ...secretKeys]);
    this.extras.set(schema.additionalProperties === false ? {} : undescribed(value, claimed));

    const rules = rulesOf(schema);
    const driverDefs: FieldDef[] = [];
    for (const [key, prop] of Object.entries(properties)) {
      if (secretKeys.has(key) || external.has(key)) {
        continue;
      }
      driverDefs.push(
        this.buildField(key, prop, required.has(key), value[key], this.configGroup, '/config', 'sf', rules.get(key)),
      );
    }

    const advancedDefs: FieldDef[] = [];
    const commonProps = COMMON_OPTIONS_SCHEMA.properties ?? {};
    for (const [key, prop] of Object.entries(commonProps)) {
      if (key in properties || external.has(key)) {
        continue; // the driver's own definition wins; the parent's keys are not ours
      }
      advancedDefs.push(this.buildField(key, prop, false, value[key], this.configGroup, '/config', 'sf-adv'));
    }

    // What the driver says about its secrets: a name and an example, and when each is shown and needed.
    // The conditions are read from the config, whose root is where the secrets' paths start.
    const secretMeta = isRecord(schema['x-secrets']) ? schema['x-secrets'] : {};
    const secretDefs: FieldDef[] = [];
    const orderedSecrets = [...secretFields, ...[...secretKeys].filter((k) => !secretFields.includes(k))];
    for (const key of orderedSecrets) {
      const prop = properties[key];
      const meta: SecretMeta | undefined = secretMeta[key];
      const control = new FormControl<string>(secretRefs[key] ?? '', {
        nonNullable: true,
        validators: [Validators.pattern(SECRET_REF_PATTERN)],
      });
      this.secretGroup.addControl(key, control, { emitEvent: false });
      const requiredWhen = conditionsOf(meta?.requiredWhen, true);
      secretDefs.push({
        key,
        id: `sf-secret-${key}`,
        pointer: `/secretRefs/${key}`,
        kind: 'secret',
        label: meta?.title ?? prop?.title ?? humanize(key),
        help: meta?.description ?? prop?.description,
        placeholder: meta?.examples?.[0] ?? `env:${toEnvName(key)}`,
        required: false,
        showWhen: conditionsOf(meta?.showWhen, true),
        rules: requiredWhen.length > 0 ? [{ when: requiredWhen, required: true }] : [],
        control,
        parent: this.configGroup,
      });
    }

    this.driverFields.set(driverDefs);
    this.advancedFields.set(advancedDefs);
    this.secretDefs.set(secretDefs);
    this.advancedOpen.set(advancedDefs.some((d) => value[d.key] !== undefined && value[d.key] !== null && value[d.key] !== ''));

    if (this.disabled()) {
      this.configGroup.disable({ emitEvent: false });
      this.secretGroup.disable({ emitEvent: false });
    }

    this.changeSub = new Subscription();
    this.changeSub.add(this.configGroup.valueChanges.subscribe(() => this.emitChange()));
    this.changeSub.add(this.secretGroup.valueChanges.subscribe(() => this.emitChange()));
    this.emitChange();
  }

  private buildField(
    key: string,
    prop: JsonSchema,
    required: boolean,
    initial: unknown,
    parent: FormGroup<Record<string, AbstractControl>>,
    parentPointer: string,
    idPrefix: string,
    rules: Rule[] = [],
  ): FieldDef {
    const base = {
      key,
      id: `${idPrefix}-${key}`,
      pointer: `${parentPointer}/${key}`,
      label: prop.title ?? humanize(key),
      help: prop.description,
      required,
      showWhen: conditionsOf(prop['x-showWhen']),
      rules,
      parent,
    };

    if (prop.const !== undefined) {
      return { ...base, kind: 'const', constValue: prop.const };
    }

    const types = typesOf(prop);

    // A required key the value leaves out starts at its schema default: the server applies
    // no default to a missing required key. A stored value always wins.
    const start = initial === undefined && required && prop.default !== undefined ? prop.default : initial;

    // An object with no properties (a field map, headers) has no fields to build: it is edited as
    // JSON text. A schema that also takes a string keeps one as typed.
    if (types.includes('object') && Object.keys(prop.properties ?? {}).length === 0) {
      const allowString = types.includes('string');
      const text = isRecord(start) ? JSON.stringify(start, null, 2) : allowString && typeof start === 'string' ? start : '';
      // Blank text is no value, as in every other field; any other string is kept as it was written.
      const stored = allowString && typeof start === 'string' && textOf(start) !== '' ? start : undefined;
      const control = new FormControl<string>(text, {
        nonNullable: true,
        validators: [jsonValidator(required, allowString)],
      });
      parent.addControl(key, control, { emitEvent: false });
      return {
        ...base,
        kind: 'json',
        allowString,
        stored,
        placeholder: JSON.stringify(prop.examples?.[0], null, 2) ?? '{ "key": "value" }',
        help: prop.description ? `${prop.description} JSON object.` : 'JSON object.',
        control,
      };
    }

    if (types.includes('object')) {
      const group = new FormGroup<Record<string, AbstractControl>>({});
      parent.addControl(key, group, { emitEvent: false });
      this.siblingSchemas.set(group, prop.properties ?? {});
      const nestedRequired = new Set(prop.required ?? []);
      const nestedValue = isRecord(initial) ? initial : {};
      const nestedRules = rulesOf(prop);
      const children = Object.entries(prop.properties ?? {}).map(([childKey, childProp]) =>
        this.buildField(
          childKey,
          childProp,
          nestedRequired.has(childKey),
          nestedValue[childKey],
          group,
          base.pointer,
          base.id,
          nestedRules.get(childKey),
        ),
      );
      // Stored keys the group does not describe are kept, unless it forbids them.
      const extras =
        prop.additionalProperties === false ? {} : undescribed(nestedValue, new Set(Object.keys(prop.properties ?? {})));
      return { ...base, kind: 'group', children, extras };
    }

    const type = types[0];

    const validators: ValidatorFn[] = [];
    if (required) {
      validators.push(Validators.required);
    }

    if (prop.enum) {
      const control = new FormControl<string | number | ''>(
        isEnumValue(start, prop.enum) ? start : '',
        { nonNullable: true, validators },
      );
      parent.addControl(key, control, { emitEvent: false });
      return {
        ...base,
        kind: 'select',
        options: prop.enum,
        defaultLabel: prop.default !== undefined ? String(prop.default) : undefined,
        control,
      };
    }

    if (type === 'boolean') {
      const control = new FormControl<boolean>(
        typeof start === 'boolean' ? start : prop.default === true,
        { nonNullable: true },
      );
      parent.addControl(key, control, { emitEvent: false });
      return { ...base, kind: 'boolean', control };
    }

    if (type === 'number' || type === 'integer') {
      if (type === 'integer') {
        validators.push(integerValidator);
      }
      if (prop.minimum !== undefined) {
        validators.push(Validators.min(prop.minimum));
      }
      if (prop.maximum !== undefined) {
        validators.push(Validators.max(prop.maximum));
      }
      if (prop.exclusiveMinimum !== undefined) {
        validators.push(exclusiveMinValidator(prop.exclusiveMinimum));
      }
      const control = new FormControl<number | null>(typeof start === 'number' ? start : null, validators);
      parent.addControl(key, control, { emitEvent: false });
      return {
        ...base,
        kind: 'number',
        step: type === 'integer' ? '1' : 'any',
        placeholder: prop.default !== undefined ? String(prop.default) : undefined,
        control,
      };
    }

    if (type === 'array') {
      const minItems = prop.minItems ?? 0;
      validators.push(arrayValidator(required, minItems));
      const initialList = Array.isArray(start) ? start.map(String).join(', ') : '';
      const control = new FormControl<string>(initialList, { nonNullable: true, validators });
      parent.addControl(key, control, { emitEvent: false });
      return {
        ...base,
        kind: 'array',
        placeholder: Array.isArray(prop.default) ? prop.default.map(String).join(', ') : 'one, two, three',
        help: prop.description ? `${prop.description} Comma separated.` : 'Comma separated.',
        control,
      };
    }

    // string
    if (prop.minLength !== undefined) {
      validators.push(Validators.minLength(prop.minLength));
    }
    if (prop.pattern) {
      validators.push(Validators.pattern(new RegExp(prop.pattern)));
    }
    const control = new FormControl<string>(typeof start === 'string' ? start : '', {
      nonNullable: true,
      validators,
    });
    parent.addControl(key, control, { emitEvent: false });
    return {
      ...base,
      kind: prop.format === 'uri' ? 'url' : 'text',
      placeholder: prop.default !== undefined && prop.default !== '' ? String(prop.default) : undefined,
      control,
    };
  }

  // ---------------------------------------------------------------------
  // Collecting
  // ---------------------------------------------------------------------

  private emitChange(): void {
    this.tick.update((n) => n + 1);
    this.changed.emit(this.snapshot());
  }

  private collect(defs: FieldDef[]): Record<string, unknown> {
    const out: Record<string, unknown> = {};
    for (const def of defs) {
      if (!this.visible(def)) {
        continue; // hidden: the control keeps its value, the config leaves it out
      }
      const v = this.valueOf(def);
      if (v !== undefined) {
        out[def.key] = v;
      }
    }
    return out;
  }

  private valueOf(def: FieldDef): unknown {
    switch (def.kind) {
      case 'const':
        return def.constValue;
      case 'group': {
        const nested = { ...def.extras, ...this.collect(def.children ?? []) };
        return Object.keys(nested).length > 0 ? nested : undefined;
      }
      case 'json': {
        // A stored string is not parsed or trimmed until the user changes it: a driver may send a string as
        // it is but compact an object (httpjson does), and parsing rounds integers a number cannot hold.
        if (def.stored !== undefined && def.control?.value === def.stored) {
          return def.stored;
        }
        const text = textOf(def.control?.value);
        if (text === '') {
          return undefined;
        }
        const parsed = parseJson(text);
        if (isRecord(parsed)) {
          return Object.keys(parsed).length > 0 ? parsed : undefined;
        }
        return def.allowString ? text : undefined;
      }
      case 'boolean':
        return def.control?.value === true;
      case 'number': {
        const v = def.control?.value;
        return typeof v === 'number' && !Number.isNaN(v) ? v : undefined;
      }
      case 'select': {
        const v = def.control?.value;
        return v === '' || v === null || v === undefined ? undefined : v;
      }
      case 'array': {
        const list = splitList(def.control?.value);
        return list.length > 0 ? list : undefined;
      }
      case 'secret':
        return undefined;
      default: {
        const v = def.control?.value;
        const s = typeof v === 'string' ? v.trim() : '';
        return s === '' ? undefined : s;
      }
    }
  }

  private collectSecrets(): Record<string, string> {
    const out: Record<string, string> = {};
    for (const def of this.secretDefs()) {
      if (!this.visible(def)) {
        continue; // hidden: the control keeps its value, the refs leave it out
      }
      const v = (def.control?.value ?? '').trim();
      if (v !== '') {
        out[def.key] = v;
      }
    }
    return out;
  }

  private allValid(defs: FieldDef[]): boolean {
    return defs.every(
      (d) =>
        !this.visible(d) ||
        ((d.control ? d.control.valid : true) && !this.conditionalErrors(d) && this.allValid(d.children ?? [])),
    );
  }

  /** `isShown` without the signal read, for code that must not track `tick`. */
  private visible(def: FieldDef, siblings: FormGroup | undefined = def.parent): boolean {
    if (!def.showWhen || !siblings) {
      return true;
    }
    return def.showWhen.every((cond) => this.matches(cond, siblings));
  }

  /** The rules of a field whose conditions all hold right now. */
  private activeRules(def: FieldDef): Rule[] {
    const base = def.parent;
    return base ? (def.rules ?? []).filter((rule) => rule.when.every((cond) => this.matches(cond, base))) : [];
  }

  /** What the rules in force find wrong with a field, in the shape of the validators' errors. */
  private conditionalErrors(def: FieldDef): ValidationErrors | null {
    const rules = this.activeRules(def);
    if (rules.length === 0) {
      return null;
    }
    const errors: ValidationErrors = {};
    if (rules.some((rule) => rule.required) && this.isEmpty(def)) {
      errors['required'] = true;
    }
    const value = def.control?.value;
    if (def.kind === 'number' && typeof value === 'number' && !Number.isNaN(value)) {
      // with no bound at all these are -Infinity and Infinity, which nothing falls outside
      const min = Math.max(...rules.flatMap((rule) => (rule.minimum === undefined ? [] : [rule.minimum])));
      const max = Math.min(...rules.flatMap((rule) => (rule.maximum === undefined ? [] : [rule.maximum])));
      if (value < min) {
        errors['min'] = { min, actual: value };
      }
      if (value > max) {
        errors['max'] = { max, actual: value };
      }
    }
    return Object.keys(errors).length > 0 ? errors : null;
  }

  /** Whether a field leaves nothing behind in the config (a secret: in secretRefs). */
  private isEmpty(def: FieldDef): boolean {
    return def.kind === 'secret' ? textOf(def.control?.value) === '' : this.valueOf(def) === undefined;
  }

  /**
   * Whether the value at the condition's path, read from `base`, is one of its values. A value left empty
   * counts as the default of its field, if the schema gives one.
   */
  private matches(cond: Condition, base: FormGroup): boolean {
    let current: unknown = base.get(cond.path)?.value;
    if (typeof current === 'string') {
      current = current.trim();
    }
    if (current === '' || current === null || current === undefined) {
      const holder = cond.path.length > 1 ? base.get(cond.path.slice(0, -1)) : base;
      current = holder instanceof FormGroup ? this.defaultOf(holder, cond.path[cond.path.length - 1]) : undefined;
    }
    return cond.values.includes(current);
  }

  /** What an empty field counts as: its schema's `const`, else its `default`. */
  private defaultOf(group: FormGroup, key: string): unknown {
    const prop = this.siblingSchemas.get(group)?.[key];
    return prop?.const !== undefined ? prop.const : prop?.default;
  }

  /** Exact pointer, else the longest field pointer the problem sits under. */
  private resolvePointer(path: string, pointers: string[]): string | null {
    if (pointers.includes(path)) {
      return path;
    }
    let best: string | null = null;
    for (const p of pointers) {
      if (path.startsWith(p + '/') && (best === null || p.length > best.length)) {
        best = p;
      }
    }
    return best;
  }
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

function humanize(key: string): string {
  const spaced = key.replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/[_-]+/g, ' ');
  return spaced.charAt(0).toUpperCase() + spaced.slice(1).toLowerCase();
}

function toEnvName(key: string): string {
  return key
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .replace(/[^A-Za-z0-9]+/g, '_')
    .toUpperCase();
}

/**
 * Reads `{ key: value | [values] }` as conditions that must all hold: the `x-showWhen` of a field, whose
 * keys are its siblings, or with `nested` the `showWhen` and `requiredWhen` of a secret, whose keys are
 * slash-separated paths from the config root (`tls/enabled`).
 */
function conditionsOf(when: unknown, nested = false): Condition[] {
  if (!isRecord(when)) {
    return [];
  }
  return Object.entries(when).map(([key, value]) => ({
    path: nested ? key.split('/') : [key],
    values: Array.isArray(value) ? value : [value],
  }));
}

/**
 * The conditional requirements an object schema states in its `allOf`, by the property they bind. The
 * form reads entries shaped `{ if: { properties: { k: { const | enum } } }, then: { required: [...],
 * properties: { p: { minimum, maximum } } } }` and nothing else of them; what it cannot read is left to
 * the server, which checks every rule.
 */
function rulesOf(schema: JsonSchema): Map<string, Rule[]> {
  const rules = new Map<string, Rule[]>();
  for (const entry of Array.isArray(schema.allOf) ? schema.allOf : []) {
    if (!isRecord(entry)) {
      continue;
    }
    const when = conditionOfIf(entry.if);
    const then = entry.then;
    if (!when || !isRecord(then)) {
      continue;
    }
    const required = Array.isArray(then.required) ? then.required : [];
    const limits = isRecord(then.properties) ? then.properties : {};
    for (const key of new Set([...required, ...Object.keys(limits)])) {
      const bounds = limits[key];
      const rule: Rule = { when: [when] };
      if (required.includes(key)) {
        rule.required = true;
      }
      if (isRecord(bounds) && typeof bounds.minimum === 'number') {
        rule.minimum = bounds.minimum;
      }
      if (isRecord(bounds) && typeof bounds.maximum === 'number') {
        rule.maximum = bounds.maximum;
      }
      if (rule.required || rule.minimum !== undefined || rule.maximum !== undefined) {
        rules.set(key, [...(rules.get(key) ?? []), rule]);
      }
    }
  }
  return rules;
}

/**
 * The one condition an `if` states: a single property tested against a `const` or an `enum`, with at most
 * a `required` that names that property. An `if` that says anything more is not one the form can read, and
 * guessing at it would ask for fields the server does not.
 */
function conditionOfIf(test: unknown): Condition | undefined {
  if (!isRecord(test) || !isRecord(test.properties)) {
    return undefined;
  }
  // any other keyword changes what the if means
  if (Object.keys(test).some((keyword) => keyword !== 'properties' && keyword !== 'required')) {
    return undefined;
  }
  const tested = Object.entries(test.properties);
  if (tested.length !== 1) {
    return undefined;
  }
  const [key, prop] = tested[0];
  const presence = test.required ?? [];
  if (!Array.isArray(presence) || presence.some((name) => name !== key) || !isRecord(prop)) {
    return undefined;
  }
  if (prop.const !== undefined) {
    return { path: [key], values: [prop.const] };
  }
  return Array.isArray(prop.enum) ? { path: [key], values: prop.enum } : undefined;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

/** `type` as a list: a schema names one type or several, and none means an object with properties, else a string. */
function typesOf(prop: JsonSchema): JsonType[] {
  return Array.isArray(prop.type) ? prop.type : [prop.type ?? (prop.properties ? 'object' : 'string')];
}

/** The entries of `value` whose keys are not in `known`. */
function undescribed(value: Record<string, unknown>, known: ReadonlySet<string>): Record<string, unknown> {
  return Object.fromEntries(Object.entries(value).filter(([key]) => !known.has(key)));
}

/** The trimmed text of a text control's value. */
function textOf(v: unknown): string {
  return typeof v === 'string' ? v.trim() : '';
}

/** `text` parsed as JSON, or `undefined` when it is not JSON (a parse never yields `undefined`). */
function parseJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

function isEnumValue(v: unknown, options: Array<string | number>): v is string | number {
  return (typeof v === 'string' || typeof v === 'number') && options.includes(v);
}

function splitList(v: unknown): string[] {
  if (typeof v !== 'string') {
    return [];
  }
  return v
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s !== '');
}

const integerValidator: ValidatorFn = (control) => {
  const v = control.value;
  if (v === null || v === undefined || v === '') {
    return null;
  }
  return Number.isInteger(v) ? null : { integer: true };
};

function exclusiveMinValidator(min: number): ValidatorFn {
  return (control) => {
    const v = control.value;
    if (v === null || v === undefined || v === '') {
      return null;
    }
    return typeof v === 'number' && v > min ? null : { exclusiveMinimum: { min, actual: v } };
  };
}

/** A JSON field holds one object. Where a string is allowed too, any other text is that string. */
function jsonValidator(required: boolean, allowString: boolean): ValidatorFn {
  return (control) => {
    const text = textOf(control.value);
    if (text === '') {
      return required ? { required: true } : null;
    }
    if (allowString) {
      return null;
    }
    const parsed = parseJson(text);
    if (parsed === undefined) {
      return { json: true };
    }
    return isRecord(parsed) ? null : { jsonObject: true };
  };
}

function arrayValidator(required: boolean, minItems: number): ValidatorFn {
  return (control) => {
    const list = splitList(control.value);
    if (list.length === 0) {
      return required ? { required: true } : null;
    }
    return list.length < minItems ? { minItems: { min: minItems, actual: list.length } } : null;
  };
}

function describeErrors(errors: ValidationErrors): string[] {
  const out: string[] = [];
  for (const [key, detail] of Object.entries(errors)) {
    switch (key) {
      case 'required':
        out.push('Required.');
        break;
      case 'min':
        out.push(`Must be at least ${detail.min}.`);
        break;
      case 'max':
        out.push(`Must be at most ${detail.max}.`);
        break;
      case 'exclusiveMinimum':
        out.push(`Must be greater than ${detail.min}.`);
        break;
      case 'minlength':
        out.push(`Must be at least ${detail.requiredLength} characters.`);
        break;
      case 'minItems':
        out.push(`Needs at least ${detail.min} item${detail.min === 1 ? '' : 's'}.`);
        break;
      case 'integer':
        out.push('Must be a whole number.');
        break;
      case 'json':
        out.push('Not valid JSON.');
        break;
      case 'jsonObject':
        out.push('Must be a JSON object, like {"key": "value"}.');
        break;
      case 'pattern':
        out.push(
          detail.requiredPattern === SECRET_REF_PATTERN.toString()
            ? 'Use a reference like env:MY_SECRET or file:/etc/sentinel/secret, never the value.'
            : 'Does not match the required format.',
        );
        break;
      default:
        out.push('Invalid value.');
    }
  }
  return out;
}
