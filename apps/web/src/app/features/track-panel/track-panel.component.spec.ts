import { ComponentFixture, TestBed } from '@angular/core/testing';
import { TrackPanelComponent } from './track-panel.component';
import { TrackPanelStore } from './track-panel.store';
import { TrackRenderService } from './track-render.service';
import { TrackApiService } from '../../core/services/track-api.service';

describe('TrackPanelComponent', () => {
  let component: TrackPanelComponent;
  let fixture: ComponentFixture<TrackPanelComponent>;
  let store: TrackPanelStore;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TrackPanelComponent],
      providers: [
        TrackPanelStore,
        { provide: TrackRenderService, useValue: { clearAll: jest.fn(), setupReplayPolylines: jest.fn(), updateReplayIndex: jest.fn() } },
        { provide: TrackApiService, useValue: { replayStream: jest.fn() } },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(TrackPanelComponent);
    component = fixture.componentInstance;
    store = TestBed.inject(TrackPanelStore);
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should not be visible when store is closed', () => {
    expect(store.isOpen()).toBe(false);
  });

  it('should toggle expanded state', () => {
    expect(store.isExpanded()).toBe(true);
    store.toggleExpanded();
    expect(store.isExpanded()).toBe(false);
  });

  it('should close and clear render service', () => {
    const renderService = TestBed.inject(TrackRenderService);
    store.open('e1', 'Test Entity', []);
    expect(store.isOpen()).toBe(true);

    component.close();
    expect(store.isOpen()).toBe(false);
    expect(renderService.clearAll).toHaveBeenCalled();
  });
});
