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
import { FeedProblem, JsonSchema } from '../../models/feed.model';
import { COMMON_OPTIONS_SCHEMA } from './common-options';

/** What the form emits whenever anything changes. */
export interface SchemaFormChange {
  config: Record<string, unknown>;
  secretRefs: Record<string, string>;
  valid: boolean;
}

export type FieldKind = 'text' | 'url' | 'number' | 'boolean' | 'select' | 'const' | 'array' | 'group' | 'secret';

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
  control?: FormControl;
  children?: FieldDef[];
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

  readonly changed = output<SchemaFormChange>();

  readonly driverFields = signal<FieldDef[]>([]);
  readonly advancedFields = signal<FieldDef[]>([]);
  readonly secretDefs = signal<FieldDef[]>([]);
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

  private changeSub: Subscription | null = null;

  constructor() {
    effect(() => {
      const schema = this.schema();
      const secretFields = this.secretFields();
      const value = this.value();
      const secretRefs = this.secretRefs();
      untracked(() => this.build(schema ?? {}, secretFields, value ?? {}, secretRefs ?? {}));
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

  get valid(): boolean {
    return this.configGroup.valid && this.secretGroup.valid;
  }

  /** Current snapshot, the same shape `changed` emits. */
  snapshot(): SchemaFormChange {
    return {
      config: this.collect([...this.driverFields(), ...this.advancedFields()]),
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
    if (!control || !control.errors || !(control.touched || control.dirty || this.showAll())) {
      return [];
    }
    return describeErrors(control.errors);
  }

  /** Server-side messages addressed to this field (or group). */
  problemsOf(def: FieldDef): string[] {
    return this.resolvedProblems().byPointer.get(def.pointer) ?? [];
  }

  hasIssue(def: FieldDef): boolean {
    return this.errorsOf(def).length > 0 || this.problemsOf(def).length > 0;
  }

  // ---------------------------------------------------------------------
  // Building
  // ---------------------------------------------------------------------

  private build(
    schema: JsonSchema,
    secretFields: string[],
    value: Record<string, unknown>,
    secretRefs: Record<string, string>,
  ): void {
    this.changeSub?.unsubscribe();
    for (const key of Object.keys(this.configGroup.controls)) {
      this.configGroup.removeControl(key, { emitEvent: false });
    }
    for (const key of Object.keys(this.secretGroup.controls)) {
      this.secretGroup.removeControl(key, { emitEvent: false });
    }
    this.showAll.set(false);

    const properties = schema.properties ?? {};
    const required = new Set(schema.required ?? []);
    const secretKeys = new Set<string>(secretFields);
    for (const [key, prop] of Object.entries(properties)) {
      if (prop['x-secret']) {
        secretKeys.add(key);
      }
    }

    const driverDefs: FieldDef[] = [];
    for (const [key, prop] of Object.entries(properties)) {
      if (secretKeys.has(key)) {
        continue;
      }
      driverDefs.push(this.buildField(key, prop, required.has(key), value[key], this.configGroup, '/config', 'sf'));
    }

    const advancedDefs: FieldDef[] = [];
    const commonProps = COMMON_OPTIONS_SCHEMA.properties ?? {};
    for (const [key, prop] of Object.entries(commonProps)) {
      if (key in properties) {
        continue; // the driver's own definition wins
      }
      advancedDefs.push(this.buildField(key, prop, false, value[key], this.configGroup, '/config', 'sf-adv'));
    }

    const secretDefs: FieldDef[] = [];
    const orderedSecrets = [...secretFields, ...[...secretKeys].filter((k) => !secretFields.includes(k))];
    for (const key of orderedSecrets) {
      const prop = properties[key];
      const control = new FormControl<string>(secretRefs[key] ?? '', {
        nonNullable: true,
        validators: [Validators.pattern(SECRET_REF_PATTERN)],
      });
      this.secretGroup.addControl(key, control, { emitEvent: false });
      secretDefs.push({
        key,
        id: `sf-secret-${key}`,
        pointer: `/secretRefs/${key}`,
        kind: 'secret',
        label: prop?.title ?? humanize(key),
        help: prop?.description,
        placeholder: `env:${toEnvName(key)}`,
        required: false,
        control,
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
  ): FieldDef {
    const base = {
      key,
      id: `${idPrefix}-${key}`,
      pointer: `${parentPointer}/${key}`,
      label: prop.title ?? humanize(key),
      help: prop.description,
      required,
    };

    if (prop.const !== undefined) {
      return { ...base, kind: 'const', constValue: prop.const };
    }

    const type = prop.type ?? (prop.properties ? 'object' : prop.enum ? 'string' : 'string');

    if (type === 'object') {
      const group = new FormGroup<Record<string, AbstractControl>>({});
      parent.addControl(key, group, { emitEvent: false });
      const nestedRequired = new Set(prop.required ?? []);
      const nestedValue = isRecord(initial) ? initial : {};
      const children = Object.entries(prop.properties ?? {}).map(([childKey, childProp]) =>
        this.buildField(childKey, childProp, nestedRequired.has(childKey), nestedValue[childKey], group, base.pointer, base.id),
      );
      return { ...base, kind: 'group', children };
    }

    const validators: ValidatorFn[] = [];
    if (required) {
      validators.push(Validators.required);
    }

    if (prop.enum) {
      const control = new FormControl<string | number | ''>(
        isEnumValue(initial, prop.enum) ? initial : '',
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
        typeof initial === 'boolean' ? initial : prop.default === true,
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
      const control = new FormControl<number | null>(typeof initial === 'number' ? initial : null, validators);
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
      const initialList = Array.isArray(initial) ? initial.map(String).join(', ') : '';
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
    const control = new FormControl<string>(typeof initial === 'string' ? initial : '', {
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
        const nested = this.collect(def.children ?? []);
        return Object.keys(nested).length > 0 ? nested : undefined;
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
      const v = (def.control?.value ?? '').trim();
      if (v !== '') {
        out[def.key] = v;
      }
    }
    return out;
  }

  private allValid(defs: FieldDef[]): boolean {
    return defs.every((d) => (d.control ? d.control.valid : true) && this.allValid(d.children ?? []));
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

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
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
