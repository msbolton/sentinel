import { ThemePreset } from '../../core/services/theme.service';

describe('Map CRT post-processing', () => {
  // Minimal mock of Cesium PostProcessStage behavior
  let stages: any[];
  let mockPostProcessStages: { add: jest.Mock; remove: jest.Mock };
  let mockScene: any;
  let mockCesium: any;

  beforeEach(() => {
    stages = [];
    mockPostProcessStages = {
      add: jest.fn((stage: any) => stages.push(stage)),
      remove: jest.fn((stage: any) => {
        const idx = stages.indexOf(stage);
        if (idx >= 0) stages.splice(idx, 1);
      }),
    };
    mockScene = {
      backgroundColor: null,
      globe: { baseColor: null },
      postProcessStages: mockPostProcessStages,
      requestRender: jest.fn(),
    };
    mockCesium = {
      Color: {
        fromCssColorString: jest.fn((css: string) => ({ css })),
      },
      PostProcessStage: jest.fn(function (this: any, opts: any) {
        this.fragmentShader = opts.fragmentShader;
      }),
    };
  });

  function applyThemeToGlobe(
    theme: ThemePreset,
    viewer: any,
    Cesium: any,
    stageRef: { current: any },
  ): void {
    if (theme === ThemePreset.CRT) {
      viewer.scene.backgroundColor = Cesium.Color.fromCssColorString('#0d0a00');
      viewer.scene.globe.baseColor = Cesium.Color.fromCssColorString('#0d0a00');
      if (!stageRef.current) {
        stageRef.current = new Cesium.PostProcessStage({ fragmentShader: 'test' });
        viewer.scene.postProcessStages.add(stageRef.current);
      }
    } else {
      viewer.scene.backgroundColor = Cesium.Color.fromCssColorString('#0a0e17');
      viewer.scene.globe.baseColor = Cesium.Color.fromCssColorString('#0a0e17');
      if (stageRef.current) {
        viewer.scene.postProcessStages.remove(stageRef.current);
        stageRef.current = null;
      }
    }
    viewer.scene.requestRender();
  }

  it('should add a PostProcessStage when switching to CRT', () => {
    const viewer = { scene: mockScene };
    const stageRef = { current: null as any };

    applyThemeToGlobe(ThemePreset.CRT, viewer, mockCesium, stageRef);

    expect(mockPostProcessStages.add).toHaveBeenCalledTimes(1);
    expect(stageRef.current).toBeTruthy();
    expect(stages.length).toBe(1);
    expect(mockScene.backgroundColor.css).toBe('#0d0a00');
  });

  it('should remove the PostProcessStage when switching back to NORMAL', () => {
    const viewer = { scene: mockScene };
    const stageRef = { current: null as any };

    applyThemeToGlobe(ThemePreset.CRT, viewer, mockCesium, stageRef);
    applyThemeToGlobe(ThemePreset.NORMAL, viewer, mockCesium, stageRef);

    expect(mockPostProcessStages.remove).toHaveBeenCalledTimes(1);
    expect(stageRef.current).toBeNull();
    expect(stages.length).toBe(0);
    expect(mockScene.backgroundColor.css).toBe('#0a0e17');
  });

  it('should not add duplicate stages on repeated CRT switches', () => {
    const viewer = { scene: mockScene };
    const stageRef = { current: null as any };

    applyThemeToGlobe(ThemePreset.CRT, viewer, mockCesium, stageRef);
    applyThemeToGlobe(ThemePreset.CRT, viewer, mockCesium, stageRef);

    expect(mockPostProcessStages.add).toHaveBeenCalledTimes(1);
    expect(stages.length).toBe(1);
  });

  it('should not error when removing with no stage active', () => {
    const viewer = { scene: mockScene };
    const stageRef = { current: null as any };

    applyThemeToGlobe(ThemePreset.NORMAL, viewer, mockCesium, stageRef);

    expect(mockPostProcessStages.remove).not.toHaveBeenCalled();
  });
});
