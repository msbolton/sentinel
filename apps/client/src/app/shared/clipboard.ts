/**
 * Copies text to the clipboard. Resolves true once the browser has taken it, and false when there
 * is no clipboard (an insecure page, an old browser) or the browser refuses, so the caller can fall
 * back to selecting the text for a manual copy.
 */
export function copyText(text: string): Promise<boolean> {
  const clipboard = typeof navigator !== 'undefined' ? navigator.clipboard : undefined;
  if (!clipboard?.writeText) {
    return Promise.resolve(false);
  }
  try {
    // Handlers are attached on the spot, so a refusal is never an unhandled rejection.
    return clipboard.writeText(text).then(
      () => true,
      () => false,
    );
  } catch {
    return Promise.resolve(false);
  }
}
