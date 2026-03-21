import {
  Component,
  ChangeDetectionStrategy,
  input,
  output,
  signal,
  HostListener,
} from '@angular/core';

export interface ContextMenuItem {
  label: string;
  action: string;
}

@Component({
  selector: 'app-context-menu',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (visible()) {
      <div
        class="context-menu"
        [style.left.px]="x()"
        [style.top.px]="y()"
      >
        @for (item of items(); track item.action) {
          <button class="context-menu-item" (click)="onItemClick($event, item)">
            {{ item.label }}
          </button>
        }
      </div>
    }
  `,
  styles: [`
    .context-menu {
      position: fixed;
      z-index: 1000;
      background: var(--surface-secondary, #1a1f2e);
      border: 1px solid var(--border-color, #2a3040);
      border-radius: 4px;
      padding: 4px 0;
      min-width: 160px;
      box-shadow: 0 4px 12px rgba(0, 0, 0, 0.4);
    }
    .context-menu-item {
      display: block;
      width: 100%;
      padding: 8px 16px;
      border: none;
      background: none;
      color: var(--text-primary, #e0e0e0);
      font-size: 13px;
      text-align: left;
      cursor: pointer;
    }
    .context-menu-item:hover {
      background: var(--surface-hover, #252a3a);
    }
  `],
})
export class ContextMenuComponent {
  items = input<ContextMenuItem[]>([]);
  itemSelected = output<string>();

  readonly visible = signal(false);
  readonly x = signal(0);
  readonly y = signal(0);

  show(x: number, y: number): void {
    this.x.set(x);
    this.y.set(y);
    this.visible.set(true);
  }

  hide(): void {
    this.visible.set(false);
  }

  onItemClick(event: Event, item: ContextMenuItem): void {
    event.stopPropagation();
    this.itemSelected.emit(item.action);
    this.hide();
  }

  @HostListener('document:click')
  @HostListener('document:keydown.escape')
  onDismiss(): void {
    this.hide();
  }
}
