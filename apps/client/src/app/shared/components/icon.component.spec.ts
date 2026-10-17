import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { ICON_NAMES, IconComponent, IconName } from './icon.component';

@Component({
  standalone: true,
  imports: [IconComponent],
  template: `<app-icon [name]="name" [size]="size" />`,
})
class HostComponent {
  name: IconName = 'globe';
  size = 20;
}

describe('IconComponent', () => {
  function render(name: IconName, size = 20): SVGElement | null {
    const fixture = TestBed.createComponent(HostComponent);
    fixture.componentInstance.name = name;
    fixture.componentInstance.size = size;
    fixture.detectChanges();
    return (fixture.nativeElement as HTMLElement).querySelector('svg');
  }

  it('renders one decorative stroke svg sized by the size input', () => {
    const svg = render('globe', 16)!;
    expect(svg).not.toBeNull();
    expect(svg.getAttribute('aria-hidden')).toBe('true');
    expect(svg.getAttribute('width')).toBe('16');
    expect(svg.getAttribute('height')).toBe('16');
    expect(svg.getAttribute('fill')).toBe('none');
    expect(svg.getAttribute('stroke')).toBe('currentColor');
  });

  it('has a drawing for every name it lists', () => {
    for (const name of ICON_NAMES) {
      const svg = render(name);
      expect(svg?.children.length ?? 0).toBeGreaterThan(0);
    }
  });
});
