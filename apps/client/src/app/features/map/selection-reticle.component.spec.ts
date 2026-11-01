import { ComponentFixture, TestBed } from '@angular/core/testing';
import { signal } from '@angular/core';
import { SelectionService } from '../../core/services/selection.service';
import { SelectionReticleComponent } from './selection-reticle.component';

describe('SelectionReticleComponent', () => {
  let fixture: ComponentFixture<SelectionReticleComponent>;
  const selection = { screenPoint: signal<{ x: number; y: number } | null>(null) };

  beforeEach(async () => {
    selection.screenPoint.set(null);
    await TestBed.configureTestingModule({
      imports: [SelectionReticleComponent],
      providers: [{ provide: SelectionService, useValue: selection }],
    }).compileComponents();
    fixture = TestBed.createComponent(SelectionReticleComponent);
    fixture.detectChanges();
  });

  afterEach(() => fixture.destroy());

  it('draws nothing while no track is selected or it has no screen position', () => {
    selection.screenPoint.set(null); fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('svg')).toBeNull();
  });

  it('draws four brackets and four reticle lines around the screen point', () => {
    selection.screenPoint.set({ x: 400, y: 300 }); fixture.detectChanges();
    expect(fixture.nativeElement.querySelectorAll('path.bracket').length).toBe(4);
    expect(fixture.nativeElement.querySelectorAll('line.reticle').length).toBe(4);
  });
});
