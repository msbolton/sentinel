import { readFileSync } from 'fs';
import { join } from 'path';

describe('Night Vision theme token coverage', () => {
  let stylesContent: string;

  beforeAll(() => {
    const stylesPath = join(__dirname, '../../styles.scss');
    stylesContent = readFileSync(stylesPath, 'utf-8');
  });

  it('should define a night-vision theme block', () => {
    expect(stylesContent).toContain('body[data-theme="night-vision"]');
  });

  it('should override every Normal theme color token in the night-vision block', () => {
    // Extract variable names from normal theme block
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

    // Extract the night-vision theme block
    const nvgBlockMatch = stylesContent.match(
      /body\[data-theme="night-vision"\]\s*\{([\s\S]*?)\n\}/
    );
    expect(nvgBlockMatch).toBeTruthy();

    const nvgBlock = nvgBlockMatch![1];

    // Every normal variable should appear in the night-vision block
    const missing: string[] = [];
    for (const varName of normalVars) {
      if (!nvgBlock.includes(`--${varName}:`)) {
        missing.push(varName);
      }
    }

    expect(missing).toEqual([]);
  });
});
