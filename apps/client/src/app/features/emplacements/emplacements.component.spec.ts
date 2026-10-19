import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { signal } from '@angular/core';
import { EmplacementsComponent } from './emplacements.component';
import { AuthService } from '../../core/services/auth.service';
import { FeedService } from '../../core/services/feed.service';

describe('EmplacementsComponent', () => {
  const auth = { hasAnyRole: jest.fn().mockReturnValue(true), getClassificationLevel: jest.fn().mockReturnValue('CONFIDENTIAL') };
  const feeds = {
    feeds: signal([
      { id: 'grpc-1', name: 'Sim', driver: 'grpc', direction: 'in' },
      { id: 'tak-out', name: 'TAK', driver: 'tak', direction: 'out' },
      { id: 'emplacements', name: 'Emplacements', driver: 'emplacement', direction: 'in' },
    ]),
    load: jest.fn(),
  };

  function create() {
    TestBed.configureTestingModule({
      imports: [EmplacementsComponent],
      providers: [provideHttpClient(), provideHttpClientTesting(), { provide: AuthService, useValue: auth }, { provide: FeedService, useValue: feeds }],
    });
    const fixture = TestBed.createComponent(EmplacementsComponent);
    const http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
    http.expectOne('/api/v1/emplacements').flush([]);
    fixture.detectChanges();
    return { fixture, http, el: fixture.nativeElement as HTMLElement };
  }

  it('offers classifications only up to the user\'s clearance', () => {
    const { fixture, el } = create();
    (el.querySelector('[aria-label="New emplacement"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    const options = Array.from(el.querySelectorAll('select[name="classification"] option')).map((o) => o.textContent?.trim());
    expect(options).toEqual(['UNCLASSIFIED', 'CONFIDENTIAL']);
  });

  it('offers only inbound connections, not the mirror itself', () => {
    const { fixture, el } = create();
    (el.querySelector('[aria-label="New emplacement"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    const options = Array.from(el.querySelectorAll('select[name="feedId"] option')).map((o) => (o as HTMLOptionElement).value);
    expect(options).toEqual(['', 'grpc-1']);
  });
});
