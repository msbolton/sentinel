import { readFileSync } from 'fs';
import { join } from 'path';

describe('CRT theme token coverage', () => {
  let stylesContent: string;

  beforeAll(() => {
    const stylesPath = join(__dirname, '../../styles.scss');
    stylesContent = readFileSync(stylesPath, 'utf-8');
  });

  it('should define a CRT theme block', () => {
    expect(stylesContent).toContain('body[data-theme="crt"]');
  });

  it('should override every Normal theme color token in the CRT block', () => {
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

    // Extract the CRT theme block
    const crtBlockMatch = stylesContent.match(
      /body\[data-theme="crt"\]\s*\{([\s\S]*?)\n\}/
    );
    expect(crtBlockMatch).toBeTruthy();

    const crtBlock = crtBlockMatch![1];

    // Every normal variable should appear in the CRT block
    const missing: string[] = [];
    for (const varName of normalVars) {
      if (!crtBlock.includes(`--${varName}:`)) {
        missing.push(varName);
      }
    }

    expect(missing).toEqual([]);
  });
});
