import {
  ChangeDetectionStrategy,
  Component,
  computed,
  effect,
  inject,
  input,
  output,
  signal,
  untracked,
} from '@angular/core';
import {
  AbstractControl,
  FormArray,
  FormControl,
  FormGroup,
  ReactiveFormsModule,
  ValidationErrors,
  ValidatorFn,
  Validators,
} from '@angular/forms';
import { Subscription } from 'rxjs';
import { FeedService } from '../../core/services/feed.service';
import { MapViewportService } from '../../core/services/map-viewport.service';
import { ClassificationLevel, levelsUpTo, normalizeClearance, rank } from '../../shared/models/classification';
import {
  AlertSeverity,
  AreaBox,
  ConnectionPolicy,
  FeedProblem,
  POLICY_MAX_BOXES,
  directionOf,
} from '../../shared/models/feed.model';

/** What the editor emits whenever anything changes. */
export interface PolicyChange {
  value: ConnectionPolicy;
  valid: boolean;
}

export const POLICY_ENTITY_TYPES: readonly string[] = [
  'PERSON',
  'VEHICLE',
  'VESSEL',
  'AIRCRAFT',
  'FACILITY',
  'EQUIPMENT',
  'UNIT',
  'SIGNAL',
  'CYBER',
  'SENSOR',
  'SATELLITE',
  'DRONE',
];

export const ALERT_SEVERITIES: readonly AlertSeverity[] = ['INFO', 'LOW', 'MEDIUM', 'HIGH', 'CRITICAL'];

const POLICY_POINTER = '/config/policy';

type BoxGroup = FormGroup<{
  north: FormControl<number | null>;
  south: FormControl<number | null>;
  east: FormControl<number | null>;
  west: FormControl<number | null>;
}>;

type BoxField = 'north' | 'south' | 'east' | 'west';

/**
 * Edits `feed.config.policy` for a sending connection: which entity types,
 * from which feeds, inside which areas, up to which classification may
 * leave, and whether alerts go too. Emits the policy with empty choices
 * left out so the server applies its defaults (everything, everywhere).
 */
@Component({
  selector: 'app-policy-editor',
  standalone: true,
  imports: [ReactiveFormsModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './policy-editor.component.html',
  styleUrl: './policy-editor.component.scss',
})
export class PolicyEditorComponent {
  private readonly feedService = inject(FeedService);
  private readonly mapViewport = inject(MapViewportService);

  readonly value = input<ConnectionPolicy | null | undefined>(undefined);
  readonly disabled = input(false);
  readonly problems = input<FeedProblem[]>([]);
  readonly clearance = input<string>('UNCLASSIFIED');

  readonly changed = output<PolicyChange>();

  readonly entityTypes = POLICY_ENTITY_TYPES;
  readonly severities = ALERT_SEVERITIES;
  readonly maxBoxes = POLICY_MAX_BOXES;
  readonly boxFields: readonly BoxField[] = ['north', 'south', 'east', 'west'];

  readonly form = new FormGroup({
    entityTypes: new FormControl<string[]>([], { nonNullable: true }),
    feeds: new FormControl<string[]>([], { nonNullable: true }),
    boxes: new FormArray<BoxGroup>([], { validators: [Validators.maxLength(POLICY_MAX_BOXES)] }),
    classificationCeiling: new FormControl<string>('', { nonNullable: true }),
    reexport: new FormControl<boolean>(false, { nonNullable: true }),
    alertsEnabled: new FormControl<boolean>(false, { nonNullable: true }),
    alertsMinSeverity: new FormControl<string>('', { nonNullable: true }),
  });

  /** Bumped on every form change so OnPush templates re-read control state. */
  readonly tick = signal(0);
  readonly showAll = signal(false);

  /** `alerts.types` is not editable here; it is carried through unchanged. */
  private alertTypes: string[] | undefined;

  /** Ceiling choices: everything up to the operator's clearance. */
  readonly ceilingOptions = computed(() => levelsUpTo(normalizeClearance(this.clearance())));

  /** A stored ceiling above the operator's clearance, kept but not selectable. */
  readonly ceilingAboveClearance = computed(() => {
    this.tick();
    const current = this.form.controls.classificationCeiling.value;
    return current !== '' && !this.ceilingOptions().includes(current as ClassificationLevel) ? current : null;
  });

  /** Feeds that produce entities (in or both), plus any stored id the list no longer has. */
  readonly feedOptions = computed(() => {
    this.tick();
    const known = this.feedService.feeds().filter((f) => directionOf(f) !== 'out');
    const selected = this.form.controls.feeds.value;
    const options = known.map((f) => ({ id: f.id, name: f.name, missing: false }));
    for (const id of selected) {
      if (!known.some((f) => f.id === id)) {
        options.push({ id, name: id, missing: true });
      }
    }
    return options;
  });

  readonly lastMapView = this.mapViewport.bounds;

  readonly boxCount = computed(() => {
    this.tick();
    return this.form.controls.boxes.length;
  });

  readonly canAddBox = computed(() => this.boxCount() < POLICY_MAX_BOXES && !this.disabled());

  /** Server problems under /config/policy that no section claims. */
  readonly unmatchedProblems = computed(() =>
    this.policyProblems().filter((p) => sectionOf(p.path) === null),
  );

  private readonly policyProblems = computed(() =>
    this.problems().filter((p) => p.path === POLICY_POINTER || p.path.startsWith(POLICY_POINTER + '/')),
  );

  private changeSub: Subscription | null = null;

  constructor() {
    effect(() => {
      const value = this.value();
      untracked(() => this.reset(value ?? {}));
    });
    effect(() => {
      const disabled = this.disabled();
      untracked(() => {
        if (disabled) {
          this.form.disable({ emitEvent: false });
        } else {
          this.form.enable({ emitEvent: false });
        }
        this.tick.update((n) => n + 1);
      });
    });
  }

  get valid(): boolean {
    return this.form.valid;
  }

  /** The policy as it would be saved. */
  snapshot(): PolicyChange {
    return { value: this.collect(), valid: this.form.valid };
  }

  /** Surfaces every validation error, e.g. when the parent's submit is refused. */
  markAllTouched(): void {
    this.form.markAllAsTouched();
    this.showAll.set(true);
    this.tick.update((n) => n + 1);
  }

  // --- entity types & feeds ---------------------------------------------

  hasType(type: string): boolean {
    this.tick();
    return this.form.controls.entityTypes.value.includes(type);
  }

  toggleType(type: string): void {
    this.toggleIn(this.form.controls.entityTypes, type);
  }

  hasFeed(id: string): boolean {
    this.tick();
    return this.form.controls.feeds.value.includes(id);
  }

  toggleFeed(id: string): void {
    this.toggleIn(this.form.controls.feeds, id);
  }

  // --- boxes ------------------------------------------------------------

  get boxes(): FormArray<BoxGroup> {
    return this.form.controls.boxes;
  }

  addBox(box?: Partial<AreaBox>): void {
    if (this.boxes.length >= POLICY_MAX_BOXES) {
      return;
    }
    this.boxes.push(makeBoxGroup(box));
    this.touch();
  }

  /** Adds the area the operator last looked at on the map. */
  useLastMapView(): void {
    const b = this.lastMapView();
    if (!b) {
      return;
    }
    this.addBox({
      north: round(b.north),
      south: round(b.south),
      east: round(b.east),
      west: round(b.west),
    });
  }

  removeBox(index: number): void {
    this.boxes.removeAt(index);
    this.touch();
  }

  boxPointer(index: number, field?: BoxField): string {
    return field ? `${POLICY_POINTER}/boxes/${index}/${field}` : `${POLICY_POINTER}/boxes/${index}`;
  }

  /** Client-side messages for one box row (its own and its fields'). */
  boxErrors(index: number): string[] {
    this.tick();
    const group = this.boxes.at(index);
    if (!group || !(group.touched || group.dirty || this.showAll())) {
      return [];
    }
    const out: string[] = [];
    for (const field of this.boxFields) {
      const control = group.controls[field];
      if (control.errors && (control.touched || control.dirty || this.showAll())) {
        out.push(...describeBoxErrors(field, control.errors));
      }
    }
    if (group.errors?.['southAboveNorth']) {
      out.push('South must not be above north.');
    }
    return out;
  }

  boxFieldHasIssue(index: number, field: BoxField): boolean {
    this.tick();
    const control = this.boxes.at(index)?.controls[field];
    const clientIssue = !!control?.errors && (control.touched || control.dirty || this.showAll());
    return clientIssue || this.problemsAt(this.boxPointer(index, field)).length > 0;
  }

  /** Server messages for one box row, including its fields. */
  boxProblems(index: number): string[] {
    return this.problemsUnder(this.boxPointer(index)).map((p) => {
      const field = p.path.slice(this.boxPointer(index).length + 1);
      return field ? `${field}: ${p.message}` : p.message;
    });
  }

  boxesError(): string | null {
    this.tick();
    return this.boxes.errors?.['maxlength'] ? `At most ${POLICY_MAX_BOXES} areas.` : null;
  }

  // --- problems -------------------------------------------------------

  /** Server messages for a whole section (entityTypes, feeds, boxes, …). */
  sectionProblems(section: string): string[] {
    const pointer = `${POLICY_POINTER}/${section}`;
    if (section === 'boxes') {
      // Row-level problems are shown on the row; keep only the list's own.
      return this.problemsAt(pointer).map((p) => p.message);
    }
    return this.problemsUnder(pointer).map((p) => p.message);
  }

  touch(): void {
    this.tick.update((n) => n + 1);
  }

  // --- internals ------------------------------------------------------

  private toggleIn(control: FormControl<string[]>, item: string): void {
    const current = control.value;
    control.setValue(current.includes(item) ? current.filter((v) => v !== item) : [...current, item]);
    control.markAsDirty();
  }

  private problemsAt(pointer: string): FeedProblem[] {
    return this.policyProblems().filter((p) => p.path === pointer);
  }

  private problemsUnder(pointer: string): FeedProblem[] {
    return this.policyProblems().filter((p) => p.path === pointer || p.path.startsWith(pointer + '/'));
  }

  private reset(value: ConnectionPolicy): void {
    this.changeSub?.unsubscribe();
    this.showAll.set(false);
    this.alertTypes = Array.isArray(value.alerts?.types) ? [...value.alerts!.types!] : undefined;

    const boxes = (value.boxes ?? []).slice(0, POLICY_MAX_BOXES);
    this.boxes.clear({ emitEvent: false });
    for (const box of boxes) {
      this.boxes.push(makeBoxGroup(box), { emitEvent: false });
    }
    this.form.reset(
      {
        boxes: boxes.map((b) => ({ north: numOrNull(b.north), south: numOrNull(b.south), east: numOrNull(b.east), west: numOrNull(b.west) })),
        entityTypes: [...(value.entityTypes ?? [])],
        feeds: [...(value.feeds ?? [])],
        classificationCeiling: value.classificationCeiling ?? '',
        reexport: value.reexport === true,
        alertsEnabled: value.alerts?.enabled === true,
        alertsMinSeverity: value.alerts?.minSeverity ?? '',
      },
      { emitEvent: false },
    );
    if (this.disabled()) {
      this.form.disable({ emitEvent: false });
    }

    this.changeSub = this.form.valueChanges.subscribe(() => this.emitChange());
    this.emitChange();
  }

  private emitChange(): void {
    this.tick.update((n) => n + 1);
    this.changed.emit(this.snapshot());
  }

  private collect(): ConnectionPolicy {
    const v = this.form.getRawValue();
    const out: ConnectionPolicy = {};
    if (v.entityTypes.length > 0) {
      out.entityTypes = [...v.entityTypes];
    }
    if (v.feeds.length > 0) {
      out.feeds = [...v.feeds];
    }
    const boxes: AreaBox[] = [];
    for (const box of v.boxes) {
      if (isNum(box.north) && isNum(box.south) && isNum(box.east) && isNum(box.west)) {
        boxes.push({ north: box.north, south: box.south, east: box.east, west: box.west });
      }
    }
    if (boxes.length > 0) {
      out.boxes = boxes;
    }
    if (v.classificationCeiling !== '') {
      out.classificationCeiling = v.classificationCeiling;
    }
    if (v.reexport) {
      out.reexport = true;
    }
    if (v.alertsEnabled || v.alertsMinSeverity !== '' || this.alertTypes) {
      out.alerts = { enabled: v.alertsEnabled };
      if (v.alertsMinSeverity !== '') {
        out.alerts.minSeverity = v.alertsMinSeverity as AlertSeverity;
      }
      if (this.alertTypes) {
        out.alerts.types = [...this.alertTypes];
      }
    }
    return out;
  }
}

/** Which editor section a /config/policy/... pointer belongs to, or null for the top of the editor. */
function sectionOf(path: string): string | null {
  const rest = path.slice(POLICY_POINTER.length + 1);
  const head = rest.split('/')[0];
  return head && ['entityTypes', 'feeds', 'boxes', 'classificationCeiling', 'reexport', 'alerts'].includes(head)
    ? head
    : null;
}

function makeBoxGroup(box?: Partial<AreaBox>): BoxGroup {
  const lat = [Validators.required, Validators.min(-90), Validators.max(90)];
  const lon = [Validators.required, Validators.min(-180), Validators.max(180)];
  return new FormGroup(
    {
      north: new FormControl<number | null>(numOrNull(box?.north), lat),
      south: new FormControl<number | null>(numOrNull(box?.south), lat),
      east: new FormControl<number | null>(numOrNull(box?.east), lon),
      west: new FormControl<number | null>(numOrNull(box?.west), lon),
    },
    { validators: [southNotAboveNorth] },
  );
}

const southNotAboveNorth: ValidatorFn = (group: AbstractControl): ValidationErrors | null => {
  const north = group.get('north')?.value;
  const south = group.get('south')?.value;
  if (isNum(north) && isNum(south) && south > north) {
    return { southAboveNorth: true };
  }
  return null;
};

function describeBoxErrors(field: BoxField, errors: ValidationErrors): string[] {
  const label = field.charAt(0).toUpperCase() + field.slice(1);
  const out: string[] = [];
  if (errors['required']) {
    out.push(`${label} is required.`);
  }
  if (errors['min']) {
    out.push(`${label} must be at least ${errors['min'].min}.`);
  }
  if (errors['max']) {
    out.push(`${label} must be at most ${errors['max'].max}.`);
  }
  return out;
}

function isNum(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v);
}

function numOrNull(v: unknown): number | null {
  return isNum(v) ? v : null;
}

function round(v: number): number {
  return Math.round(v * 10_000) / 10_000;
}

/** Exported for the parent: whether a stored level sits above the operator's. */
export function aboveClearance(level: string | undefined, clearance: string): boolean {
  return !!level && rank(level) > rank(clearance);
}
