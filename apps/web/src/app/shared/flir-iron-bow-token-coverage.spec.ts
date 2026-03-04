import { readFileSync } from 'fs';
import { join } from 'path';

describe('FLIR Iron Bow theme token coverage', () => {
  let stylesContent: string;

  beforeAll(() => {
    const stylesPath = join(__dirname, '../../styles.scss');
    stylesContent = readFileSync(stylesPath, 'utf-8');
  });

  it('should define a flir-iron-bow theme block', () => {
    expect(stylesContent).toContain('body[data-theme="flir-iron-bow"]');
  });

  it('should override every Normal theme color token in the flir-iron-bow block', () => {
    const normalBlockMatch = stylesContent.match(
      /:root,\s*\nbody\[data-theme="normal"\]\s*\{([\s\S]*?)\n\}/
    );
    expect(normalBlockMatch).toBeTruthy();

    const normalBlock = normalBlockMatch![1];
    const varPattern = /--([\w-]+):/g;
    const normalVars: string[] = [];
    let match;
    while ((match = varPattern.exec(normalBlock)) !== null) {
      normalVars.push(match[1]);
    }

    expect(normalVars.length).toBeGreaterThan(0);

    const ironBowBlockMatch = stylesContent.match(
      /body\[data-theme="flir-iron-bow"\]\s*\{([\s\S]*?)\n\}/
    );
    expect(ironBowBlockMatch).toBeTruthy();

    const ironBowBlock = ironBowBlockMatch![1];

    const missing: string[] = [];
    for (const varName of normalVars) {
      if (!ironBowBlock.includes(`--${varName}:`)) {
        missing.push(varName);
      }
    }

    expect(missing).toEqual([]);
  });
});
