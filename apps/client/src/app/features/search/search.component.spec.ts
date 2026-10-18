import { ComponentFixture, TestBed, fakeAsync, tick } from '@angular/core/testing';
import { of } from 'rxjs';
import { SearchComponent } from './search.component';
import { EntityService } from '../../core/services/entity.service';
import { EntityFocusService, FocusTarget } from '../../core/services/entity-focus.service';
import { Classification, Entity, EntitySource, EntityType } from '../../shared/models/entity.model';

const vessel = {
  id: 'v1', name: 'PONTUS HIGHWAY', entityType: EntityType.VESSEL, source: EntitySource.AIS, classification: Classification.UNCLASSIFIED,
  position: { latitude: 50.63, longitude: -1.01 }, metadata: {}, affiliations: [], createdAt: '', updatedAt: '',
} as unknown as Entity;

describe('SearchComponent', () => {
  let fixture: ComponentFixture<SearchComponent>;
  let el: HTMLElement;
  let focused: FocusTarget[];

  beforeEach(fakeAsync(() => {
    TestBed.configureTestingModule({
      imports: [SearchComponent],
      providers: [{ provide: EntityService, useValue: { searchEntities: () => of({ data: [vessel], total: 1 }) } }],
    });
    focused = [];
    TestBed.inject(EntityFocusService).focus$.subscribe((t) => focused.push(t));
    fixture = TestBed.createComponent(SearchComponent);
    el = fixture.nativeElement;
    fixture.detectChanges();
    fixture.componentInstance.onSearchInput('pontus');
    tick(300);
    fixture.detectChanges();
  }));

  it('shows a result on the map when it is clicked or Enter is pressed on it', () => {
    const card = el.querySelector('.entity-card') as HTMLElement;
    expect(card.getAttribute('role')).toBe('button');
    expect(card.getAttribute('aria-label')).toBe('Show PONTUS HIGHWAY on the map');
    card.click();
    expect(focused).toEqual([{ entityId: 'v1', position: { latitude: 50.63, longitude: -1.01 }, label: 'PONTUS HIGHWAY' }]);
    card.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(focused.length).toBe(2);
  });
});
