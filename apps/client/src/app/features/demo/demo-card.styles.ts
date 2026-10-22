/**
 * The look the demo's two cards share, built from the app's own tokens (panel
 * background, borders, radii, buttons) so they read as part of Sentinel.
 * Full width with a 16 px gutter on a phone, at most 420 px wide above that.
 */
export const DEMO_CARD_STYLES = `
  :host {
    display: contents;
  }

  .scrim {
    position: fixed;
    inset: 0;
    z-index: 1100;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
    background: rgba(5, 8, 14, 0.55);
    backdrop-filter: blur(2px);
    -webkit-backdrop-filter: blur(2px);
    animation: demoFadeIn 200ms ease-out;
  }

  .card {
    width: 100%;
    max-width: 420px;
    max-height: 100%;
    overflow-y: auto;
    padding: 20px;
    box-sizing: border-box;
    color: var(--text-primary);
    background: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-lg);
    animation: demoRiseIn 250ms cubic-bezier(0.16, 1, 0.3, 1);
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 12px;
    font-family: var(--font-mono);
    font-size: 0.7rem;
    letter-spacing: 0.14em;
    text-transform: uppercase;
    color: var(--text-secondary);
  }

  h2 {
    margin: 0 0 12px;
    font-size: 1.25rem;
    font-weight: 600;
    line-height: 1.3;
  }

  p {
    margin: 0 0 16px;
    font-size: 0.9rem;
    line-height: 1.5;
  }

  .actions {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .actions .btn {
    width: 100%;
    padding: 9px 14px;
    font-size: 0.9rem;
  }

  .actions .btn:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .back {
    display: inline-block;
    margin-top: 16px;
    font-size: 0.8rem;
    color: var(--text-secondary);
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .back:hover {
    color: var(--text-primary);
  }

  button:focus-visible,
  a:focus-visible {
    outline: 2px solid var(--accent-cyan);
    outline-offset: 2px;
  }

  @keyframes demoFadeIn {
    from { opacity: 0; }
    to { opacity: 1; }
  }

  @keyframes demoRiseIn {
    from { opacity: 0; transform: translateY(8px); }
    to { opacity: 1; transform: translateY(0); }
  }

  @media (prefers-reduced-motion: reduce) {
    .scrim,
    .card {
      animation: none;
    }
  }
`;

/** Where the cards send a visitor to leave the demo. */
export const PORTFOLIO_URL = 'https://msbolton.dev';
