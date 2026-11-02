import { ComponentFixture, TestBed } from '@angular/core/testing';
import { signal } from '@angular/core';
import { ActivatedRoute, convertToParamMap } from '@angular/router';
import { BehaviorSubject, of, throwError } from 'rxjs';
import { LinkGraphComponent } from './link-graph.component';
import { EntityService } from '../../core/services/entity.service';
import { LinkService } from '../../core/services/link.service';
import { SearchService } from '../../core/services/search.service';
import { SelectionService } from '../../core/services/selection.service';
import { Classification, Entity, EntityType } from '../../shared/models/entity.model';
import { GraphResult, LINK_TYPES, LinkType } from '../../shared/models/link.model';

const centre = { entityId: 'c', entityType: EntityType.VESSEL, name: 'Centre', classification: Classification.UNCLASSIFIED };
const neighbour = {
  entityId: 'n',
  entityType: EntityType.PERSON,
  name: 'Neighbour',
  classification: Classification.UNCLASSIFIED,
  position: { lat: 1, lon: 2 },
};
const linked: GraphResult = {
  nodes: [centre, neighbour],
  edges: [{ id: 'e1', sourceEntityId: 'c', targetEntityId: 'n', linkType: LinkType.ASSOCIATION, confidence: 0.5 }],
};

describe('LinkGraphComponent', () => {
  let links: { graph: jest.Mock; create: jest.Mock; delete: jest.Mock };
  let entities: { getEntityById: jest.Mock; getEntity: jest.Mock };
  let selection: { selectById: jest.Mock; select: jest.Mock; entity: ReturnType<typeof signal<Entity | null>> };
  let queryParams: BehaviorSubject<ReturnType<typeof convertToParamMap>>;

  async function create(): Promise<ComponentFixture<LinkGraphComponent>> {
    await TestBed.configureTestingModule({
      imports: [LinkGraphComponent],
      providers: [
        { provide: LinkService, useValue: links },
        { provide: EntityService, useValue: entities },
        { provide: SelectionService, useValue: selection },
        { provide: SearchService, useValue: { search: jest.fn(() => of({ total: 0, page: 1, pageSize: 8, hits: [] })) } },
        { provide: ActivatedRoute, useValue: { queryParamMap: queryParams } },
      ],
    }).compileComponents();
    const fixture = TestBed.createComponent(LinkGraphComponent);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
    return fixture;
  }

  const text = (f: ComponentFixture<LinkGraphComponent>): string => f.nativeElement.textContent;

  beforeEach(() => {
    // jsdom has no canvas, so vis-network stays out of these tests.
    jest.spyOn(LinkGraphComponent.prototype as any, 'loadVis').mockResolvedValue(null);
    links = { graph: jest.fn(() => of(linked)), create: jest.fn(), delete: jest.fn() };
    entities = { getEntityById: jest.fn(() => undefined), getEntity: jest.fn() };
    selection = { selectById: jest.fn(() => false), select: jest.fn(), entity: signal<Entity | null>(null) };
    queryParams = new BehaviorSubject(convertToParamMap({ entity: 'c' }));
  });

  it('loads the graph around the entity in the route and reports its size', async () => {
    const f = await create();
    expect(links.graph).toHaveBeenCalledWith({ centerId: 'c', maxDepth: 2 });
    expect(text(f)).toContain('2 entities / 1 links');
    expect(f.componentInstance.center()).toEqual({ id: 'c', name: 'Centre' });
  });

  it('asks only for the chosen link types and depth', async () => {
    const f = await create();
    links.graph.mockClear();
    f.componentInstance.toggleLinkType(LinkType.ASSOCIATION);
    f.componentInstance.setDepth(4);
    expect(links.graph).toHaveBeenLastCalledWith({
      centerId: 'c',
      maxDepth: 4,
      types: LINK_TYPES.filter((t) => t !== LinkType.ASSOCIATION),
    });
  });

  it('says when the centre has no links yet', async () => {
    links.graph.mockReturnValue(of({ nodes: [centre], edges: [] }));
    const f = await create();
    expect(text(f)).toContain('Centre has no links yet');
  });

  it('says when the service is unavailable', async () => {
    links.graph.mockReturnValue(throwError(() => new Error('503')));
    const f = await create();
    expect(text(f)).toContain('Link analysis is unavailable');
    expect(f.componentInstance.loading()).toBe(false);
  });

  it('shows a node on the map from the cache, the service, or its own position', async () => {
    const f = await create();
    const c = f.componentInstance;

    selection.selectById.mockReturnValueOnce(true);
    c.showOnMap(neighbour);
    expect(entities.getEntity).not.toHaveBeenCalled();

    const live = { id: 'n', name: 'Neighbour live' } as Entity;
    entities.getEntity.mockReturnValueOnce(of(live));
    c.showOnMap(neighbour);
    expect(selection.select).toHaveBeenLastCalledWith(live);

    entities.getEntity.mockReturnValueOnce(throwError(() => new Error('404')));
    c.showOnMap(neighbour);
    expect(selection.select).toHaveBeenLastCalledWith(
      expect.objectContaining({ id: 'n', name: 'Neighbour', position: { latitude: 1, longitude: 2 } }),
    );
  });

  it('saves a link from the centre to the picked entity and reloads the graph', async () => {
    links.create.mockReturnValue(of({ id: 'e2' }));
    const f = await create();
    const c = f.componentInstance;
    c.startLink();
    c.onLinkTargetPicked({ id: 'n', name: 'Neighbour', entityType: EntityType.PERSON });
    c.linkType.set(LinkType.COMMUNICATION);
    c.linkConfidence.set(0.8);
    links.graph.mockClear();
    c.saveLink();
    expect(links.create).toHaveBeenCalledWith({ sourceEntityId: 'c', targetEntityId: 'n', linkType: 'COMMUNICATION', confidence: 0.8 });
    expect(links.graph).toHaveBeenCalledTimes(1);
    expect(c.addingLink()).toBe(false);
  });

  it('links from the selected node and refuses a link to itself', async () => {
    const f = await create();
    const c = f.componentInstance;
    c.selectedNode.set(neighbour);
    c.startLink();
    expect(c.linkSource()).toEqual({ id: 'n', name: 'Neighbour' });
    c.onLinkTargetPicked({ id: 'n', name: 'Neighbour', entityType: EntityType.PERSON });
    expect(c.canSaveLink()).toBe(false);
    c.onLinkTargetPicked({ id: 'c', name: 'Centre', entityType: EntityType.VESSEL });
    expect(c.canSaveLink()).toBe(true);
  });

  it('explains a link the server refused', async () => {
    links.create.mockReturnValue(throwError(() => ({ status: 404 })));
    const f = await create();
    const c = f.componentInstance;
    c.startLink();
    c.onLinkTargetPicked({ id: 'x', name: 'Hidden', entityType: EntityType.PERSON });
    c.saveLink();
    f.detectChanges();
    expect(text(f)).toContain('not available to you');
  });

  it('removes the selected link and reloads the graph', async () => {
    links.delete.mockReturnValue(of(undefined));
    const f = await create();
    const c = f.componentInstance;
    c.selectedEdge.set(linked.edges[0]);
    links.graph.mockClear();
    c.removeLink(linked.edges[0]);
    expect(links.delete).toHaveBeenCalledWith('e1');
    expect(c.selectedEdge()).toBeNull();
    expect(links.graph).toHaveBeenCalledTimes(1);
  });
});
