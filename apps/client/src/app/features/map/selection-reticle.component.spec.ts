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

  it('frames a 40px box with 8px arms and stops the lines 40px short of the point', () => {
    selection.screenPoint.set({ x: 400, y: 300 }); fixture.detectChanges();
    const left = fixture.nativeElement.querySelector('line.reticle') as SVGLineElement;
    expect(left.getAttribute('x2')).toBe('360.5');
    const brackets = Array.from(fixture.nativeElement.querySelectorAll('path.bracket')).map((b) => (b as SVGPathElement).getAttribute('d'));
    expect(brackets).toContain('M 380.5 288.5 V 280.5 H 388.5');
  });

  it('keeps the reticle lines inside the overlay when the point is near an edge', () => {
    selection.screenPoint.set({ x: 10, y: 10 }); fixture.detectChanges();
    const lines = Array.from(fixture.nativeElement.querySelectorAll('line.reticle')) as SVGLineElement[];
    for (const l of lines) {
      for (const a of ['x1', 'y1', 'x2', 'y2']) expect(Number(l.getAttribute(a))).toBeGreaterThanOrEqual(0);
    }
    expect(lines[0].getAttribute('x2')).toBe('0');
  });
});
