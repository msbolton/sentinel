import { TrackRenderService } from './track-render.service';

describe('TrackRenderService', () => {
  let service: TrackRenderService;
  let mockViewer: any;
  let mockCesium: any;

  beforeEach(() => {
    service = new TrackRenderService();
    mockViewer = {
      entities: {
        add: jest.fn().mockReturnValue({}),
        remove: jest.fn(),
      },
    };
    mockCesium = {
      Cartesian3: { fromDegrees: jest.fn().mockReturnValue({}) },
      Color: {
        CYAN: { withAlpha: jest.fn().mockReturnValue('cyan-alpha') },
        GRAY: { withAlpha: jest.fn().mockReturnValue('gray-alpha') },
        YELLOW: {},
        RED: {},
      },
      PolylineGlowMaterialProperty: jest.fn().mockReturnValue({}),
      CallbackProperty: jest.fn().mockImplementation((cb) => cb),
    };
    service.init(mockViewer, mockCesium);
  });

  it('should initialize with viewer and Cesium references', () => {
    expect(service).toBeDefined();
  });

  it('should draw static track polyline with start/end markers', () => {
    const points = [
      { id: '1', entityId: 'e1', latitude: 10, longitude: 20, altitude: 100, heading: null, speedKnots: null, course: null, source: null, timestamp: '2025-01-01T00:00:00Z' },
      { id: '2', entityId: 'e1', latitude: 11, longitude: 21, altitude: 200, heading: null, speedKnots: null, course: null, source: null, timestamp: '2025-01-01T01:00:00Z' },
    ];
    service.drawStaticTrack(points);
    // polyline + start marker + end marker = 3 entities
    expect(mockViewer.entities.add).toHaveBeenCalledTimes(3);
  });

  it('should clear all track entities', () => {
    const points = [
      { id: '1', entityId: 'e1', latitude: 10, longitude: 20, altitude: 100, heading: null, speedKnots: null, course: null, source: null, timestamp: '2025-01-01T00:00:00Z' },
      { id: '2', entityId: 'e1', latitude: 11, longitude: 21, altitude: 200, heading: null, speedKnots: null, course: null, source: null, timestamp: '2025-01-01T01:00:00Z' },
    ];
    service.drawStaticTrack(points);
    service.clearAll();
    expect(mockViewer.entities.remove).toHaveBeenCalledTimes(3);
  });

  it('should update replay index', () => {
    service.updateReplayIndex(5);
    // No crash - the index is stored for CallbackProperty to read
    expect(service).toBeDefined();
  });
});
