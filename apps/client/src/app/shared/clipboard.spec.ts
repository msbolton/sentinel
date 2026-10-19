import { copyText } from './clipboard';

describe('copyText', () => {
  function setClipboard(value: unknown): void {
    Object.defineProperty(navigator, 'clipboard', { value, configurable: true });
  }

  afterEach(() => {
    // jsdom has no clipboard; take the stand-in away again.
    delete (navigator as unknown as Record<string, unknown>)['clipboard'];
  });

  it('writes the text and resolves true', async () => {
    const writeText = jest.fn().mockResolvedValue(undefined);
    setClipboard({ writeText });

    await expect(copyText('echo hi')).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith('echo hi');
  });

  it('resolves false when the browser refuses', async () => {
    setClipboard({ writeText: jest.fn().mockRejectedValue(new DOMException('denied', 'NotAllowedError')) });
    await expect(copyText('x')).resolves.toBe(false);

    setClipboard({
      writeText: () => {
        throw new Error('not in a user gesture');
      },
    });
    await expect(copyText('x')).resolves.toBe(false);
  });

  it('resolves false when there is no clipboard to write to', async () => {
    expect(navigator.clipboard).toBeUndefined();
    await expect(copyText('x')).resolves.toBe(false);

    setClipboard({});
    await expect(copyText('x')).resolves.toBe(false);
  });
});
