import { Injectable, signal } from '@angular/core';
import { layerKeyOf } from '../../shared/models/feed.model';

const STORAGE_KEY = 'sentinel-layer-visibility';
/** Pre-driver-model key that held per-user feed on/off overrides. */
const LEGACY_STORAGE_KEY = 'sentinel-feed-preferences';

/**
 * Which map layers this user has hidden, keyed by `layerKeyOf(entity)`
 * (the feed id, else the legacy source). Pure client state persisted in
 * localStorage: hiding a layer never touches the feeds API, so one
 * analyst's choice is invisible to everyone else.
 */
@Injectable({ providedIn: 'root' })
export class LayerVisibilityService {
  private readonly hiddenState = signal<ReadonlySet<string>>(new Set());

  /** Layer keys this user has hidden. */
  readonly hidden = this.hiddenState.asReadonly();

  constructor() {
    this.removeLegacyPreferences();
    this.hiddenState.set(this.read());
  }

  isHidden(key: string): boolean {
    return this.hiddenState().has(key);
  }

  isVisible(entityOrKey: string | { feedId?: string | null; source?: string | null }): boolean {
    const key = typeof entityOrKey === 'string' ? entityOrKey : layerKeyOf(entityOrKey);
    return !this.hiddenState().has(key);
  }

  toggle(key: string): void {
    if (this.isHidden(key)) {
      this.show(key);
    } else {
      this.hide(key);
    }
  }

  hide(key: string): void {
    if (this.hiddenState().has(key)) {
      return;
    }
    const next = new Set(this.hiddenState());
    next.add(key);
    this.commit(next);
  }

  show(key: string): void {
    if (!this.hiddenState().has(key)) {
      return;
    }
    const next = new Set(this.hiddenState());
    next.delete(key);
    this.commit(next);
  }

  showAll(): void {
    if (this.hiddenState().size === 0) {
      return;
    }
    this.commit(new Set());
  }

  private commit(next: ReadonlySet<string>): void {
    this.hiddenState.set(next);
    this.write(next);
  }

  private read(): Set<string> {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (!raw) {
        return new Set();
      }
      const parsed: unknown = JSON.parse(raw);
      if (!Array.isArray(parsed)) {
        return new Set();
      }
      return new Set(parsed.filter((k): k is string => typeof k === 'string' && k.length > 0));
    } catch {
      return new Set();
    }
  }

  private write(keys: ReadonlySet<string>): void {
    try {
      if (keys.size === 0) {
        localStorage.removeItem(STORAGE_KEY);
      } else {
        localStorage.setItem(STORAGE_KEY, JSON.stringify([...keys]));
      }
    } catch {
      // Private windows and blocked storage: the in-memory state still applies.
    }
  }

  private removeLegacyPreferences(): void {
    try {
      localStorage.removeItem(LEGACY_STORAGE_KEY);
    } catch {
      // Nothing to clean up if storage is unavailable.
    }
  }
}
