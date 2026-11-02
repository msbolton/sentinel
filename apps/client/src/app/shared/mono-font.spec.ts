import { monoFont } from './mono-font';

describe('monoFont', () => {
  it('falls back to monospace when --font-mono is not defined', () => {
    expect(monoFont()).toBe('monospace');
  });

  it('reads --font-mono from the root element once it is defined', () => {
    document.documentElement.style.setProperty('--font-mono', "'IBM Plex Mono', monospace");
    expect(monoFont()).toBe("'IBM Plex Mono', monospace");
    document.documentElement.style.removeProperty('--font-mono');
  });
});
