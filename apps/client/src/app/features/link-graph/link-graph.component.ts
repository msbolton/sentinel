import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  ElementRef,
  NgZone,
  OnDestroy,
  OnInit,
  computed,
  inject,
  signal,
  viewChild,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute } from '@angular/router';
import { Subscription } from 'rxjs';
import { IconComponent } from '../../shared/components/icon.component';
import { EntityService } from '../../core/services/entity.service';
import { LinkService } from '../../core/services/link.service';
import { SelectionService } from '../../core/services/selection.service';
import { SearchHit } from '../../shared/models/search.model';
import { GraphEdge, GraphNode, GraphResult, LINK_TYPES, LinkType, nodeToEntity } from '../../shared/models/link.model';
import { EntityPickerComponent } from './entity-picker.component';
import { linkTypeLabel, nodeColor, toVisGraph } from './graph-view';

/** The route query parameter that opens the panel on an entity. */
export const ENTITY_QUERY_PARAM = 'entity';
const DEFAULT_DEPTH = 2;
const DEFAULT_CONFIDENCE = 0.5;

// vis-network is loaded on demand; its types stay out of the bundle.
type Network = any;
type DataSet = any;

/**
 * The links around one entity, walked breadth-first by the link-analysis
 * service and drawn by vis-network. Nodes select their track on the map;
 * analysts add links here, since nothing else creates them yet.
 */
@Component({
  selector: 'app-link-graph',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, IconComponent, EntityPickerComponent],
  templateUrl: './link-graph.component.html',
  styleUrls: ['./link-graph.component.scss'],
})
export class LinkGraphComponent implements OnInit, OnDestroy {
  private readonly links = inject(LinkService);
  private readonly entities = inject(EntityService);
  private readonly selection = inject(SelectionService);
  private readonly route = inject(ActivatedRoute);
  private readonly ngZone = inject(NgZone);
  private readonly destroyRef = inject(DestroyRef);

  readonly graphContainer = viewChild.required<ElementRef<HTMLDivElement>>('graphContainer');

  /** The entity the graph is centred on. */
  center = signal<{ id: string; name: string } | null>(null);
  maxDepth = signal<number>(DEFAULT_DEPTH);
  linkTypes = LINK_TYPES;
  selectedLinkTypes = signal<Set<LinkType>>(new Set(LINK_TYPES));

  loading = signal<boolean>(false);
  failed = signal<boolean>(false);
  graph = signal<GraphResult | null>(null);
  selectedNode = signal<GraphNode | null>(null);
  selectedEdge = signal<GraphEdge | null>(null);

  nodeCount = computed(() => this.graph()?.nodes.length ?? 0);
  edgeCount = computed(() => this.graph()?.edges.length ?? 0);
  /** The graph loaded but holds nothing beyond the centre. */
  isEmpty = computed(() => this.graph() !== null && this.edgeCount() === 0);

  /** The add-link form. The source is the selected node, else the centre. */
  addingLink = signal<boolean>(false);
  linkTarget = signal<SearchHit | null>(null);
  linkType = signal<LinkType>(LinkType.ASSOCIATION);
  linkConfidence = signal<number>(DEFAULT_CONFIDENCE);
  linkSaving = signal<boolean>(false);
  linkError = signal<string | null>(null);
  linkSource = computed<{ id: string; name: string } | null>(() => {
    const node = this.selectedNode();
    return node ? { id: node.entityId, name: node.name } : this.center();
  });

  private network: Network | null = null;
  private vis: any = null;
  private visReady: Promise<void> | null = null;
  private readonly subscriptions = new Subscription();

  ngOnInit(): void {
    this.subscriptions.add(
      this.route.queryParamMap.subscribe((params) => {
        const id = params.get(ENTITY_QUERY_PARAM);
        if (id && id !== this.center()?.id) {
          const cached = this.entities.getEntityById(id);
          this.setCenter(id, cached?.name ?? id);
        }
      }),
    );
    this.destroyRef.onDestroy(() => this.subscriptions.unsubscribe());
  }

  ngOnDestroy(): void {
    this.network?.destroy();
    this.network = null;
  }

  /** vis-network, loaded once on first use; null where it cannot load. */
  protected loadVis(): Promise<any> {
    return import('vis-network/standalone').catch(() => null);
  }

  onCenterPicked(hit: SearchHit): void {
    this.setCenter(hit.id, hit.name);
  }

  private setCenter(id: string, name: string): void {
    this.center.set({ id, name });
    this.selectedNode.set(null);
    this.selectedEdge.set(null);
    this.loadGraph();
  }

  setDepth(depth: number): void {
    this.maxDepth.set(depth);
    this.loadGraph();
  }

  toggleLinkType(type: LinkType): void {
    this.selectedLinkTypes.update((current) => {
      const next = new Set(current);
      if (next.has(type)) next.delete(type);
      else next.add(type);
      return next;
    });
    this.loadGraph();
  }

  loadGraph(): void {
    const center = this.center();
    if (!center) return;
    const types = [...this.selectedLinkTypes()];
    this.loading.set(true);
    this.failed.set(false);
    this.subscriptions.add(
      this.links
        .graph({
          centerId: center.id,
          maxDepth: this.maxDepth(),
          ...(types.length < this.linkTypes.length ? { types } : {}),
        })
        .subscribe({
          next: (graph) => {
            this.graph.set(graph);
            this.loading.set(false);
            this.pruneSelection(graph);
            const centerNode = graph.nodes.find((n) => n.entityId === center.id);
            if (centerNode && centerNode.name !== center.name) this.center.set({ id: center.id, name: centerNode.name });
            void this.render(graph, center.id);
          },
          error: () => {
            this.graph.set(null);
            this.loading.set(false);
            this.failed.set(true);
          },
        }),
    );
  }

  private async render(graph: GraphResult, centerId: string): Promise<void> {
    this.visReady ??= this.loadVis().then((vis) => {
      this.vis = vis;
    });
    await this.visReady;
    if (!this.vis || this.graph() !== graph) return;

    // vis-network draws on a canvas, so it needs the resolved font, not var().
    const fontFace = getComputedStyle(document.documentElement).getPropertyValue('--font-mono').trim() || 'monospace';
    const { nodes, edges } = toVisGraph(graph, centerId, fontFace);
    const nodeById = new Map(graph.nodes.map((n) => [n.entityId, n]));
    const edgeById = new Map(graph.edges.map((e) => [e.id, e]));

    this.ngZone.runOutsideAngular(() => {
      this.network?.destroy();
      const nodesDataSet: DataSet = new this.vis.DataSet(nodes);
      const edgesDataSet: DataSet = new this.vis.DataSet(edges);
      this.network = new this.vis.Network(
        this.graphContainer().nativeElement,
        { nodes: nodesDataSet, edges: edgesDataSet },
        {
          physics: {
            enabled: true,
            solver: 'forceAtlas2Based',
            forceAtlas2Based: {
              gravitationalConstant: -50,
              centralGravity: 0.005,
              springLength: 150,
              springConstant: 0.08,
              damping: 0.4,
              avoidOverlap: 0.8,
            },
            stabilization: { enabled: true, iterations: 200, updateInterval: 25 },
          },
          interaction: { hover: true, tooltipDelay: 200, keyboard: { enabled: true }, zoomView: true, dragView: true },
          nodes: { borderWidthSelected: 3 },
          edges: { selectionWidth: 2 },
          layout: { improvedLayout: true },
        },
      );

      this.network.on('click', (params: { nodes: string[]; edges: string[] }) => {
        const node = params.nodes.length ? nodeById.get(params.nodes[0]) ?? null : null;
        const edge = !node && params.edges.length ? edgeById.get(params.edges[0]) ?? null : null;
        this.ngZone.run(() => {
          this.selectedNode.set(node);
          this.selectedEdge.set(edge);
        });
      });
      this.network.on('doubleClick', (params: { nodes: string[] }) => {
        const node = params.nodes.length ? nodeById.get(params.nodes[0]) : undefined;
        if (node) this.ngZone.run(() => this.expand(node));
      });
    });
  }

  /** Drops a selection the reloaded graph no longer holds. */
  private pruneSelection(graph: GraphResult): void {
    const node = this.selectedNode();
    if (node && !graph.nodes.some((n) => n.entityId === node.entityId)) this.selectedNode.set(null);
    const edge = this.selectedEdge();
    if (edge && !graph.edges.some((e) => e.id === edge.id)) this.selectedEdge.set(null);
  }

  /** Re-centres the graph on a node. */
  expand(node: GraphNode): void {
    this.setCenter(node.entityId, node.name);
  }

  /**
   * Selects the node's track on the map: the live one when the map has it,
   * else the entity service's copy, else the node's own position.
   */
  showOnMap(node: GraphNode): void {
    if (this.selection.selectById(node.entityId)) return;
    const fallback = nodeToEntity(node);
    this.subscriptions.add(
      this.entities.getEntity(node.entityId).subscribe({
        next: (live) => this.selection.select(live),
        error: () => this.selection.select(fallback),
      }),
    );
  }

  nodeName(id: string): string {
    return this.graph()?.nodes.find((n) => n.entityId === id)?.name ?? id;
  }

  dismiss(): void {
    this.selectedNode.set(null);
    this.selectedEdge.set(null);
    this.network?.unselectAll();
  }

  startLink(): void {
    this.linkTarget.set(null);
    this.linkError.set(null);
    this.addingLink.set(true);
  }

  cancelLink(): void {
    this.addingLink.set(false);
    this.linkTarget.set(null);
    this.linkError.set(null);
  }

  onLinkTargetPicked(hit: SearchHit): void {
    this.linkTarget.set(hit);
    this.linkError.set(null);
  }

  canSaveLink(): boolean {
    const source = this.linkSource();
    const target = this.linkTarget();
    return !!source && !!target && source.id !== target.id && !this.linkSaving();
  }

  saveLink(): void {
    const source = this.linkSource();
    const target = this.linkTarget();
    if (!source || !target || !this.canSaveLink()) return;
    this.linkSaving.set(true);
    this.linkError.set(null);
    this.subscriptions.add(
      this.links
        .create({
          sourceEntityId: source.id,
          targetEntityId: target.id,
          linkType: this.linkType(),
          confidence: this.linkConfidence(),
        })
        .subscribe({
          next: () => {
            this.linkSaving.set(false);
            this.cancelLink();
            this.loadGraph();
          },
          error: (err: { status?: number }) => {
            this.linkSaving.set(false);
            this.linkError.set(
              err.status === 404 ? 'One of the entities is not available to you.' : 'The link could not be saved. Try again.',
            );
          },
        }),
    );
  }

  removeLink(edge: GraphEdge): void {
    this.subscriptions.add(
      this.links.delete(edge.id).subscribe({
        next: () => {
          this.selectedEdge.set(null);
          this.loadGraph();
        },
        error: () => this.linkError.set('The link could not be removed. Try again.'),
      }),
    );
  }

  zoomIn(): void {
    this.network?.moveTo({ scale: this.network.getScale() * 1.3 });
  }

  zoomOut(): void {
    this.network?.moveTo({ scale: this.network.getScale() / 1.3 });
  }

  fitGraph(): void {
    this.network?.fit({ animation: { duration: 500, easingFunction: 'easeInOutQuad' } });
  }

  nodeColor = nodeColor;
  linkTypeLabel = linkTypeLabel;
  confidencePercent(value: number): string {
    return `${Math.round(value * 100)}%`;
  }
}
