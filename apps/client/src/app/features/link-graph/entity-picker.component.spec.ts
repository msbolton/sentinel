import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of } from 'rxjs';
import { EntityPickerComponent } from './entity-picker.component';
import { SearchService } from '../../core/services/search.service';
import { EntityType } from '../../shared/models/entity.model';
import { SearchHit } from '../../shared/models/search.model';

describe('EntityPickerComponent', () => {
  let search: jest.Mock;
  let fixture: ComponentFixture<EntityPickerComponent>;
  let picked: SearchHit[];

  const hits = (): HTMLButtonElement[] => Array.from(fixture.nativeElement.querySelectorAll('.picker-hit'));

  function type(text: string): void {
    fixture.componentInstance.onInput(text);
    fixture.detectChanges();
    jest.advanceTimersByTime(300);
    fixture.detectChanges();
  }

  beforeEach(async () => {
    jest.useFakeTimers();
    search = jest.fn(() =>
      of({
        total: 2,
        page: 1,
        pageSize: 8,
        hits: [
          { id: 'a', name: 'UAL123', entityType: EntityType.AIRCRAFT },
          { id: 'b', name: 'UAL456', entityType: EntityType.AIRCRAFT },
        ],
      }),
    );
    await TestBed.configureTestingModule({
      imports: [EntityPickerComponent],
      providers: [{ provide: SearchService, useValue: { search } }],
    }).compileComponents();
    fixture = TestBed.createComponent(EntityPickerComponent);
    picked = [];
    fixture.componentInstance.picked.subscribe((h) => picked.push(h));
    fixture.detectChanges();
  });

  afterEach(() => jest.useRealTimers());

  it('searches after the debounce and lists the hits', () => {
    type('ua');
    expect(search).toHaveBeenCalledWith({ q: 'ua', pageSize: 8 });
    expect(hits().map((h) => h.querySelector('.picker-hit-name')?.textContent)).toEqual(['UAL123', 'UAL456']);
  });

  it('picks a hit on click and shows its name', () => {
    type('ua');
    hits()[1].click();
    fixture.detectChanges();
    expect(picked).toEqual([{ id: 'b', name: 'UAL456', entityType: EntityType.AIRCRAFT }]);
    expect(fixture.componentInstance.query()).toBe('UAL456');
    expect(hits()).toHaveLength(0);
  });

  it('picks a pasted entity id on enter without searching', () => {
    const id = '11111111-1111-4111-8111-111111111111';
    type(id);
    expect(search).not.toHaveBeenCalled();
    fixture.componentInstance.onEnter();
    expect(picked).toEqual([{ id, name: id, entityType: EntityType.UNKNOWN }]);
  });

  it('says when nothing matches', () => {
    search.mockReturnValue(of({ total: 0, page: 1, pageSize: 8, hits: [] }));
    type('zzz');
    expect(fixture.nativeElement.textContent).toContain('No entity matches "zzz"');
  });
});
