import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { BehaviorSubject, of } from 'rxjs';
import { LayersPillComponent } from './layers-pill.component';
import { AuthService } from '../../core/services/auth.service';
import { EntityService } from '../../core/services/entity.service';
import { FeedService } from '../../core/services/feed.service';
import { Feed, FeedDirection } from '../models/feed.model';

function makeFeed(id: string, direction?: FeedDirection): Feed {
  return {
    id,
    name: id.toUpperCase(),
    description: '',
    driver: 'x',
    source: 'OPENSKY',
    builtin: false,
    enabled: true,
    version: 1,
    config: {},
    secretRefs: {},
    capabilities: [],
    direction,
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    status: {
      owner: 'ingest-0',
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

describe('LayersPillComponent', () => {
  let fixture: ComponentFixture<LayersPillComponent>;
  let el: HTMLElement;
  let httpMock: HttpTestingController;

  beforeEach(async () => {
    localStorage.clear();
    await TestBed.configureTestingModule({
      imports: [LayersPillComponent],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter([]),
        { provide: EntityService, useValue: { currentEntities$: of(new Map()) } },
        {
          provide: AuthService,
          useValue: { userProfile$: new BehaviorSubject({ username: 'op', roles: ['sentinel-operator'], classificationLevel: 'UNCLASSIFIED' }) },
        },
      ],
    }).compileComponents();
    httpMock = TestBed.inject(HttpTestingController);
    TestBed.inject(FeedService).load().subscribe();
    httpMock.expectOne('/api/v1/feeds').flush([makeFeed('opensky', 'in'), makeFeed('tak-hq', 'both'), makeFeed('kml-push', 'out'), makeFeed('legacy')]);

    fixture = TestBed.createComponent(LayersPillComponent);
    el = fixture.nativeElement as HTMLElement;
    fixture.detectChanges();
  });

  afterEach(() => {
    fixture.destroy();
    httpMock.verify();
  });

  it('lists a row per feed that produces entities, leaving out send-only connections', () => {
    el.querySelector<HTMLButtonElement>('.pill-header')!.click();
    fixture.detectChanges();
    httpMock.expectOne('/api/v1/feeds').flush([makeFeed('opensky', 'in'), makeFeed('tak-hq', 'both'), makeFeed('kml-push', 'out'), makeFeed('legacy')]);
    fixture.detectChanges();

    const names = Array.from(el.querySelectorAll('.layer-name')).map((n) => n.textContent?.trim());
    expect(names).toEqual(['OPENSKY', 'TAK-HQ', 'LEGACY']);
    expect(fixture.componentInstance.visibleCount()).toBe(3);

    const link = el.querySelector<HTMLAnchorElement>('.manage-link');
    expect(link?.textContent?.trim()).toBe('MANAGE CONNECTIONS →');
    expect(link?.getAttribute('href')).toBe('/connections');
  });
});
