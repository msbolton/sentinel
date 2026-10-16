import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { FeedsComponent } from './feeds.component';
import { Feed, FeedDriver } from '../../shared/models/feed.model';

function makeFeed(over: Partial<Feed> & { id: string }): Feed {
  return {
    name: over.id,
    description: '',
    driver: 'opensky',
    source: 'OPENSKY',
    builtin: false,
    enabled: true,
    version: 1,
    config: {},
    secretRefs: {},
    capabilities: ['viewport'],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    status: {
      owner: 'ingest-0',
      state: 'running',
      health: 'healthy',
      lastMessageAt: new Date(Date.now() - 15_000).toISOString(),
      lastSuccessAt: null,
      messagesTotal: 1234,
      entitiesLast: 56,
      errorsTotal: 0,
      appliedVersion: 1,
      detail: { creditsRemaining: 3900, idle: true },
      updatedAt: null,
    },
    ...over,
  };
}

const drivers: FeedDriver[] = [
  {
    id: 'opensky',
    name: 'OpenSky Network',
    description: 'ADS-B states',
    source: 'OPENSKY',
    capabilities: ['viewport'],
    configSchema: { type: 'object', properties: { intervalSec: { type: 'integer', minimum: 1 } } },
    secretFields: ['clientId', 'clientSecret'],
  },
];

describe('FeedsComponent', () => {
  let fixture: ComponentFixture<FeedsComponent>;
  let component: FeedsComponent;
  let el: HTMLElement;
  let httpMock: HttpTestingController;

  function flushInitial(feeds: Feed[]): void {
    httpMock.expectOne('/api/v1/feeds').flush(feeds);
    httpMock.expectOne('/api/v1/drivers').flush(drivers);
    fixture.detectChanges();
  }

  function rowOf(id: string): HTMLTableRowElement {
    const rows = Array.from(el.querySelectorAll<HTMLTableRowElement>('tr.feed-row'));
    const row = rows.find((r) => r.querySelector('.feed-id')?.textContent?.trim() === id);
    if (!row) {
      throw new Error(`no row for ${id}`);
    }
    return row;
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [FeedsComponent],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    httpMock = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(FeedsComponent);
    component = fixture.componentInstance;
    el = fixture.nativeElement as HTMLElement;
    fixture.detectChanges();
  });

  afterEach(() => {
    fixture.destroy();
    httpMock.verify();
  });

  it('lists feeds with driver name, state, health, counters and relative time', () => {
    flushInitial([
      makeFeed({ id: 'opensky-global', name: 'OpenSky global', builtin: true, description: 'Everything' }),
      makeFeed({
        id: 'custom',
        enabled: false,
        status: {
          ...makeFeed({ id: 'x' }).status,
          state: 'error',
          health: 'critical',
          lastError: 'boom',
          errorsTotal: 3,
          lastMessageAt: null,
          detail: {},
        },
      }),
    ]);

    const builtin = rowOf('opensky-global');
    expect(builtin.querySelector('.feed-name')?.textContent).toContain('OpenSky global');
    expect(builtin.querySelector('.tag')?.textContent?.trim()).toBe('built-in');
    expect(builtin.querySelector('.feed-description')?.textContent?.trim()).toBe('Everything');
    expect(builtin.cells[2].textContent?.trim()).toBe('OpenSky Network');
    expect(builtin.cells[3].textContent?.trim()).toBe('OPENSKY');
    expect(builtin.cells[4].textContent?.trim()).toBe('ingest-0');
    expect(builtin.querySelector('.state-badge')?.textContent?.trim()).toBe('Running');
    expect(builtin.querySelector('.health-dot')?.classList.contains('health-healthy')).toBe(true);
    expect(builtin.querySelector('.highlights')?.textContent?.trim()).toBe('idle · 3900 credits');
    expect(builtin.cells[6].textContent?.trim()).toMatch(/^1[0-9]s ago$/);
    expect(builtin.querySelector('.col-counters')?.textContent?.replace(/\s+/g, '')).toBe('1,234/56/0');
    expect(builtin.querySelector<HTMLInputElement>('.switch input')?.checked).toBe(true);

    const deleteBtn = builtin.querySelector<HTMLButtonElement>('.btn-delete');
    expect(deleteBtn?.disabled).toBe(true);
    expect(deleteBtn?.parentElement?.getAttribute('title')).toBe('Built-in feeds cannot be deleted');

    const custom = rowOf('custom');
    expect(custom.querySelector('.state-badge')?.textContent?.trim()).toBe('Error');
    expect(custom.querySelector('.health-dot')?.classList.contains('health-critical')).toBe(true);
    expect(custom.querySelector('.last-error')?.textContent?.trim()).toBe('boom');
    expect(custom.cells[6].textContent?.trim()).toBe('never');
    expect(custom.querySelector('.errors')?.textContent?.trim()).toBe('3');
    expect(custom.querySelector<HTMLInputElement>('.switch input')?.checked).toBe(false);
    expect(custom.querySelector<HTMLButtonElement>('.btn-delete')?.disabled).toBe(false);
  });

  it('expands a row to show status and driver detail', () => {
    flushInitial([makeFeed({ id: 'a', status: { ...makeFeed({ id: 'a' }).status, lastError: 'rate limited' } })]);

    expect(el.querySelector('tr.detail-row')).toBeNull();
    rowOf('a').querySelector<HTMLButtonElement>('.expand-btn')?.click();
    fixture.detectChanges();

    const detail = el.querySelector('tr.detail-row');
    expect(detail?.textContent).toContain('rate limited');
    expect(detail?.textContent).toContain('creditsRemaining');
    expect(detail?.textContent).toContain('3900');
  });

  it('toggles enabled optimistically and rolls back on error', () => {
    flushInitial([makeFeed({ id: 'a', name: 'Feed A' })]);

    const checkbox = rowOf('a').querySelector<HTMLInputElement>('.switch input')!;
    checkbox.checked = false;
    checkbox.dispatchEvent(new Event('change'));
    fixture.detectChanges();

    expect(rowOf('a').querySelector<HTMLInputElement>('.switch input')?.checked).toBe(false);
    const req = httpMock.expectOne('/api/v1/feeds/a/disable');
    expect(req.request.method).toBe('POST');
    req.flush({ error: 'nope' }, { status: 500, statusText: 'Server Error' });
    fixture.detectChanges();

    expect(rowOf('a').querySelector<HTMLInputElement>('.switch input')?.checked).toBe(true);
    expect(el.querySelector('.banner-error')?.textContent).toContain('Could not disable Feed A: nope');
  });

  it('applies the server response after a successful toggle', () => {
    flushInitial([makeFeed({ id: 'a' })]);

    component.toggleEnabled(component.rows()[0].feed, false);
    httpMock.expectOne('/api/v1/feeds/a/disable').flush(makeFeed({ id: 'a', enabled: false, version: 2 }));
    fixture.detectChanges();

    expect(component.rows()[0].feed.version).toBe(2);
    expect(rowOf('a').querySelector<HTMLInputElement>('.switch input')?.checked).toBe(false);
  });

  it('deletes after an inline confirmation', () => {
    flushInitial([makeFeed({ id: 'a' }), makeFeed({ id: 'b' })]);

    rowOf('a').querySelector<HTMLButtonElement>('.btn-delete')?.click();
    fixture.detectChanges();
    const confirm = rowOf('a').querySelector('.confirm');
    expect(confirm?.textContent).toContain('Delete?');

    // "No" keeps the row
    Array.from(confirm!.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === 'No')?.click();
    fixture.detectChanges();
    expect(rowOf('a').querySelector('.confirm')).toBeNull();

    rowOf('a').querySelector<HTMLButtonElement>('.btn-delete')?.click();
    fixture.detectChanges();
    rowOf('a').querySelector<HTMLButtonElement>('.confirm .btn-danger')?.click();
    fixture.detectChanges();

    const req = httpMock.expectOne('/api/v1/feeds/a');
    expect(req.request.method).toBe('DELETE');
    req.flush(null);
    fixture.detectChanges();

    expect(el.querySelectorAll('tr.feed-row').length).toBe(1);
    expect(() => rowOf('a')).toThrow();
  });

  it('surfaces a refused delete', () => {
    flushInitial([makeFeed({ id: 'a', name: 'Feed A' })]);

    component.askDelete(component.rows()[0].feed);
    component.confirmDelete(component.rows()[0].feed);
    httpMock.expectOne('/api/v1/feeds/a').flush({ error: 'built-in feeds cannot be deleted' }, { status: 403, statusText: 'Forbidden' });
    fixture.detectChanges();

    expect(el.querySelectorAll('tr.feed-row').length).toBe(1);
    expect(el.querySelector('.banner-error')?.textContent).toContain('Could not delete Feed A');
  });

  it('opens the drawer in create and edit mode', () => {
    flushInitial([makeFeed({ id: 'a', name: 'Feed A' })]);
    expect(el.querySelector('app-feed-form')).toBeNull();

    (Array.from(el.querySelectorAll<HTMLButtonElement>('.page-header .btn')).find((b) => b.textContent?.includes('Add feed')))?.click();
    fixture.detectChanges();
    expect(el.querySelector('app-feed-form')).toBeTruthy();
    expect(el.querySelector('app-feed-form h2')?.textContent).toContain('choose a driver');

    component.closeDrawer();
    fixture.detectChanges();
    expect(el.querySelector('app-feed-form')).toBeNull();

    Array.from(rowOf('a').querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === 'Edit')?.click();
    fixture.detectChanges();
    expect(el.querySelector('app-feed-form h2')?.textContent).toContain('Edit Feed A');
  });

  it('releases the poll on destroy', () => {
    flushInitial([]);
    fixture.destroy();
    // no further /api/v1/feeds request is pending; verify() in afterEach confirms it
  });
});
