import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { LinkService } from './link.service';
import { LinkType } from '../../shared/models/link.model';

describe('LinkService', () => {
  let service: LinkService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    service = TestBed.inject(LinkService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('asks for a graph with the server parameter names', () => {
    service.graph({ centerId: 'c', maxDepth: 2, types: [LinkType.ASSOCIATION, LinkType.FINANCIAL], minConfidence: 0.4 }).subscribe();
    const p = http.expectOne((r) => r.url === '/api/v1/links/graph').request.params;
    expect(p.get('centerId')).toBe('c');
    expect(p.get('maxDepth')).toBe('2');
    expect(p.get('types')).toBe('ASSOCIATION,FINANCIAL');
    expect(p.get('minConfidence')).toBe('0.4');
  });

  it('sends only the center when nothing else is set', () => {
    service.graph({ centerId: 'c' }).subscribe();
    expect(http.expectOne((r) => r.url === '/api/v1/links/graph').request.params.keys()).toEqual(['centerId']);
  });

  it('creates and deletes links', () => {
    service.create({ sourceEntityId: 'a', targetEntityId: 'b', linkType: LinkType.COMMUNICATION, confidence: 0.8 }).subscribe();
    const post = http.expectOne({ method: 'POST', url: '/api/v1/links' });
    expect(post.request.body).toEqual({ sourceEntityId: 'a', targetEntityId: 'b', linkType: 'COMMUNICATION', confidence: 0.8 });
    service.delete('l1').subscribe();
    http.expectOne({ method: 'DELETE', url: '/api/v1/links/l1' });
  });
});
