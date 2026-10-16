import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { EntityDetailPanelComponent } from './entity-detail-panel.component';
import { Classification, Entity, EntitySource, EntityType } from '../models/entity.model';
import { Feed } from '../models/feed.model';

function makeEntity(over: Partial<Entity> = {}): Entity {
  return {
    id: 'e1',
    entityType: EntityType.UNIT,
    name: 'ALPHA 1',
    source: 'COT' as EntitySource,
    classification: Classification.UNCLASSIFIED,
    metadata: {},
    affiliations: [],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    ...over,
  };
}

function makeFeed(id: string, name: string): Feed {
  return {
    id,
    name,
    description: '',
    driver: 'tak',
    source: 'COT',
    builtin: false,
    enabled: true,
    version: 1,
    config: {},
    secretRefs: {},
    capabilities: [],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    status: {
      owner: '',
      state: 'running',
      health: 'healthy',
      lastMessageAt: null,
      lastSuccessAt: null,
      messagesTotal: 0,
      entitiesLast: 0,
      errorsTotal: 0,
      appliedVersion: 1,
      detail: {},
      updatedAt: null,
    },
  };
}

describe('EntityDetailPanelComponent', () => {
  let fixture: ComponentFixture<EntityDetailPanelComponent>;
  let el: HTMLElement;
  let httpMock: HttpTestingController;

  function rows(): Record<string, string> {
    const out: Record<string, string> = {};
    el.querySelectorAll('.origin-section .field-row').forEach((row) => {
      const label = row.querySelector('.field-label')?.textContent?.trim() ?? '';
      out[label] = (row.querySelector('.field-value, .chip, .source-list')?.textContent ?? '').replace(/\s+/g, ' ').trim();
    });
    return out;
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [EntityDetailPanelComponent],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    httpMock = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(EntityDetailPanelComponent);
    el = fixture.nativeElement as HTMLElement;
  });

  afterEach(() => httpMock.verify());

  it('shows the origin rows for an entity that came through TAK', () => {
    fixture.componentRef.setInput(
      'entity',
      makeEntity({
        feedId: 'tak-hq',
        identities: [{ type: 'COT_UID', value: 'ANDROID-1234' }],
        platformData: { cot: { uid: 'ANDROID-1234', cotType: 'a-f-G-U-C', callsign: 'VIPER' } },
        sources: [
          { feedId: 'tak-hq', source: 'COT' },
          { feedId: 'opensky', source: 'OPENSKY', lastObservedAt: '2026-10-01T00:00:00Z' },
        ],
      }),
    );
    fixture.detectChanges();

    expect(el.querySelector('.origin-section .section-label')?.textContent).toBe('Origin & Timing');
    // the feed list is fetched once so the connection can be named
    expect(rows()['Connection']).toBe('tak-hq');
    httpMock.expectOne('/api/v1/feeds').flush([makeFeed('tak-hq', 'TAK HQ'), makeFeed('opensky', 'OpenSky')]);
    fixture.detectChanges();

    const r = rows();
    expect(r['Connection']).toBe('TAK HQ');
    expect(r['Source']).toBe('COT');
    expect(r['TAK uid']).toBe('ANDROID-1234');
    expect(r['Callsign']).toBe('VIPER');
    const contributing = Array.from(el.querySelectorAll('.source-list li')).map((li) =>
      Array.from(li.querySelectorAll('span')).map((span) => span.textContent?.trim()),
    );
    expect(contributing).toEqual([
      ['TAK HQ', 'COT'],
      ['OpenSky', 'OPENSKY'],
    ]);
    expect(el.querySelector('.chip-classification')?.textContent?.trim()).toBe('UNCLASSIFIED');
  });

  it('falls back to platforms.cot.callsign, then the name, and hides what it does not have', () => {
    fixture.componentRef.setInput(
      'entity',
      makeEntity({
        identities: [{ type: 'COT_UID', value: 'U-1' }],
        platforms: { cot: { callsign: 'HAWK' } },
        sources: [{ feedId: 'tak-hq', source: 'COT' }],
      }),
    );
    fixture.detectChanges();

    let r = rows();
    expect(r['Connection']).toBeUndefined();
    expect(r['Callsign']).toBe('HAWK');
    expect(r['Contributing feeds']).toBeUndefined();

    fixture.componentRef.setInput('entity', makeEntity({ identities: [{ type: 'COT_UID', value: 'U-2' }] }));
    fixture.detectChanges();
    r = rows();
    expect(r['TAK uid']).toBe('U-2');
    expect(r['Callsign']).toBe('ALPHA 1');

    fixture.componentRef.setInput('entity', makeEntity({ identities: [{ type: 'ICAO24', value: 'abc123' }] }));
    fixture.detectChanges();
    r = rows();
    expect(r['TAK uid']).toBeUndefined();
    expect(r['Callsign']).toBeUndefined();
    httpMock.expectNone('/api/v1/feeds');
  });
});
