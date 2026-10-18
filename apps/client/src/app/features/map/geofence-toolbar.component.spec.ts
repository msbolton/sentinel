import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { GeofenceToolbarComponent } from './geofence-toolbar.component';
import { addPoint, DrawSession, newSession } from './geofence-draw-state';
import { GeofenceShape } from '../../shared/models/geofence.model';

@Component({
  standalone: true,
  imports: [GeofenceToolbarComponent],
  template: `<app-geofence-toolbar [session]="session()" (shapeChange)="shapes.push($event)" (colorChange)="colors.push($event)" (radiusChange)="radii.push($event)" (finish)="finished = finished + 1" (cancel)="cancelled = cancelled + 1" />`,
})
class HostComponent {
  session = signal<DrawSession>(newSession('polygon', '#f97316'));
  shapes: GeofenceShape[] = [];
  colors: string[] = [];
  radii: number[] = [];
  finished = 0;
  cancelled = 0;
}

describe('GeofenceToolbarComponent', () => {
  let fixture: ComponentFixture<HostComponent>;
  let host: HostComponent;
  let el: HTMLElement;

  const shapeButton = (label: string) =>
    Array.from(el.querySelectorAll<HTMLButtonElement>('[role="group"][aria-label="Shape"] button')).find((b) => b.textContent?.trim() === label)!;
  const field = (label: string) => el.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!;
  const type = (input: HTMLInputElement, value: string) => {
    input.value = value;
    input.dispatchEvent(new Event('input'));
    input.dispatchEvent(new Event('change'));
    fixture.detectChanges();
  };

  beforeEach(() => {
    fixture = TestBed.createComponent(HostComponent);
    host = fixture.componentInstance;
    el = fixture.nativeElement as HTMLElement;
    fixture.detectChanges();
  });

  it('marks the current shape pressed and emits a change on click', () => {
    expect(shapeButton('Polygon').getAttribute('aria-pressed')).toBe('true');
    expect(shapeButton('Circle').getAttribute('aria-pressed')).toBe('false');
    shapeButton('Circle').click();
    expect(host.shapes).toEqual(['circle']);
  });

  it('emits a normalised colour from the hex field and ignores an invalid one', () => {
    type(field('Custom colour hex'), 'F97316');
    expect(host.colors).toEqual(['#f97316']);
    type(field('Custom colour hex'), 'xyz');
    expect(host.colors).toEqual(['#f97316']);
    expect(field('Custom colour hex').value).toBe('f97316');
    el.querySelector<HTMLButtonElement>('button[aria-label="Cyan"]')!.click();
    expect(host.colors).toEqual(['#f97316', '#06b6d4']);
  });

  it('keeps the last valid radius when the field is blank, zero or text', () => {
    host.session.set(newSession('circle', '#06b6d4', 8000));
    fixture.detectChanges();
    const radius = field('Radius in kilometres');
    expect(radius.value).toBe('8.0');
    type(radius, '0');
    type(radius, '');
    type(radius, 'abc');
    expect(host.radii).toEqual([]);
    type(radius, '12.5');
    expect(host.radii).toEqual([12500]);
    host.session.set(newSession('polygon', '#06b6d4'));
    fixture.detectChanges();
    expect(el.querySelector('input[aria-label="Radius in kilometres"]')).toBeNull();
  });

  it('disables Finish with the reason while the shape cannot finish', () => {
    const finish = () => Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === 'Finish')!;
    expect(finish().disabled).toBe(true);
    expect(finish().title).toBe('Add at least three points');
    host.session.set(addPoint(addPoint(addPoint(newSession('polygon', '#f97316'), [55, 25]), [55.1, 25]), [55.1, 25.1]));
    fixture.detectChanges();
    expect(finish().disabled).toBe(false);
    finish().click();
    expect(host.finished).toBe(1);
  });

  it('finishes on Enter and cancels on Escape outside fields', () => {
    host.session.set(addPoint(addPoint(addPoint(newSession('polygon', '#f97316'), [55, 25]), [55.1, 25]), [55.1, 25.1]));
    fixture.detectChanges();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(host.finished).toBe(1);
    const hex = field('Custom colour hex');
    hex.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(host.finished).toBe(1);
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(host.cancelled).toBe(1);
  });
});
