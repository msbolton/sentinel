import {
  Component,
  OnInit,
  OnDestroy,
  AfterViewInit,
  ViewChild,
  ElementRef,
  signal,
  NgZone,
  ChangeDetectionStrategy,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { HttpClient, HttpParams } from '@angular/common/http';
import { Subscription } from 'rxjs';
import {
  GraphData,
  GraphNode,
  GraphEdge,
  LinkType,
} from '../../shared/models/link.model';
import { EntityType } from '../../shared/models/entity.model';

// vis-network type stubs for dynamic import
type Network = any;
type DataSet = any;

@Component({
  selector: 'app-link-graph',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, FormsModule],
  templateUrl: './link-graph.component.html',
  styleUrls: ['./link-graph.component.scss'],
})
export class LinkGraphComponent implements OnInit, OnDestroy, AfterViewInit {
  @ViewChild('graphContainer', { static: true })
  graphContainer!: ElementRef<HTMLDivElement>;

  entityIdInput = signal<string>('');
  depth = signal<number>(2);
  loading = signal<boolean>(false);
  selectedNode = signal<GraphNode | null>(null);
  graphLoaded = signal<boolean>(false);
  nodeCount = signal<number>(0);
  edgeCount = signal<number>(0);

  linkTypes = Object.values(LinkType);
  selectedLinkTypes = signal<Set<LinkType>>(new Set(Object.values(LinkType)));

  private network: Network | null = null;
  private nodesDataSet: DataSet | null = null;
  private edgesDataSet: DataSet | null = null;
  private vis: any = null;
  private subscriptions = new Subscription();

  constructor(
    private readonly ngZone: NgZone,
    private readonly http: HttpClient,
  ) {}

  ngOnInit(): void {}

  async ngAfterViewInit(): Promise<void> {
    try {
      this.vis = await import('vis-network/standalone');
    } catch {
      console.warn('[LinkGraph] vis-network not available');
    }
  }

  ngOnDestroy(): void {
    this.subscriptions.unsubscribe();
    if (this.network) {
      this.network.destroy();
      this.network = null;
    }
  }

  loadGraph(): void {
    const entityId = this.entityIdInput().trim();
    if (!entityId) return;

    this.loading.set(true);

    let params = new HttpParams()
      .set('entityId', entityId)
      .set('depth', this.depth().toString());

    const linkTypes = [...this.selectedLinkTypes()];
    if (linkTypes.length < this.linkTypes.length) {
      params = params.set('linkTypes', linkTypes.join(','));
    }

    this.http
      .get<GraphData>('/api/v1/links/graph', { params })
      .subscribe({
        next: (data) => {
          this.renderGraph(data);
          this.loading.set(false);
          this.graphLoaded.set(true);
        },
        error: () => {
          this.loading.set(false);
        },
      });
  }

  private renderGraph(data: GraphData): void {
    if (!this.vis) {
      console.warn('[LinkGraph] vis-network not loaded');
      return;
    }

    this.ngZone.runOutsideAngular(() => {
      // Destroy existing network
      if (this.network) {
        this.network.destroy();
      }

      // Prepare nodes
      const nodes = data.nodes.map((node) => ({
        ...node,
        color: this.getNodeColor(node.entityType),
        shape: 'dot',
        size: 16,
        font: {
          color: '#e0e6ed',
          size: 12,
          face: 'JetBrains Mono, monospace',
        },
        borderWidth: 2,
        shadow: {
          enabled: true,
          color: 'rgba(0, 0, 0, 0.3)',
          size: 8,
        },
      }));

      // Prepare edges
      const edges = data.edges.map((edge) => ({
        ...edge,
        color: this.getEdgeColor(edge.label),
        width: Math.max(1, (edge.value ?? 50) / 25),
        arrows: { to: { enabled: true, scaleFactor: 0.5 } },
        font: {
          color: '#5a6a80',
          size: 10,
          face: 'JetBrains Mono, monospace',
          align: 'middle',
          strokeWidth: 3,
          strokeColor: '#0a0e17',
        },
        smooth: {
          type: 'continuous',
          roundness: 0.2,
        },
      }));

      this.nodesDataSet = new this.vis.DataSet(nodes);
      this.edgesDataSet = new this.vis.DataSet(edges);

      this.nodeCount.set(nodes.length);
      this.edgeCount.set(edges.length);

      const options = {
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
          stabilization: {
            enabled: true,
            iterations: 200,
            updateInterval: 25,
          },
        },
        interaction: {
          hover: true,
          tooltipDelay: 200,
          navigationButtons: true,
          keyboard: {
            enabled: true,
          },
          zoomView: true,
          dragView: true,
        },
        nodes: {
          borderWidthSelected: 3,
          chosen: {
            node: (values: any) => {
              values.size = 20;
              values.borderWidth = 3;
            },
          },
        },
        edges: {
          selectionWidth: 2,
        },
        layout: {
          improvedLayout: true,
        },
      };

      this.network = new this.vis.Network(
        this.graphContainer.nativeElement,
        { nodes: this.nodesDataSet, edges: this.edgesDataSet },
        options,
      );

      // Event handlers
      this.network.on('click', (params: any) => {
        if (params.nodes.length > 0) {
          const nodeId = params.nodes[0];
          const node = this.nodesDataSet.get(nodeId);
          this.ngZone.run(() => {
            this.selectedNode.set(node);
          });
        } else {
          this.ngZone.run(() => {
            this.selectedNode.set(null);
          });
        }
      });

      this.network.on('doubleClick', (params: any) => {
        if (params.nodes.length > 0) {
          const nodeId = params.nodes[0];
          // Expand node - load graph centered on this node
          this.ngZone.run(() => {
            this.entityIdInput.set(nodeId);
            this.loadGraph();
          });
        }
      });
    });
  }

  toggleLinkType(type: LinkType): void {
    const current = new Set(this.selectedLinkTypes());
    if (current.has(type)) {
      current.delete(type);
    } else {
      current.add(type);
    }
    this.selectedLinkTypes.set(current);
  }

  zoomIn(): void {
    if (this.network) {
      const scale = this.network.getScale();
      this.network.moveTo({ scale: scale * 1.3 });
    }
  }

  zoomOut(): void {
    if (this.network) {
      const scale = this.network.getScale();
      this.network.moveTo({ scale: scale / 1.3 });
    }
  }

  fitGraph(): void {
    if (this.network) {
      this.network.fit({ animation: { duration: 500, easingFunction: 'easeInOutQuad' } });
    }
  }

  togglePhysics(): void {
    if (this.network) {
      const physics = this.network.physics;
      // Toggle physics
      this.network.setOptions({
        physics: { enabled: !physics?.options?.enabled },
      });
    }
  }

  private getNodeColor(entityType: string): { background: string; border: string; highlight: { background: string; border: string } } {
    const colorMap: Record<string, string> = {
      [EntityType.PERSON]: '#3b82f6',
      [EntityType.VEHICLE]: '#10b981',
      [EntityType.VESSEL]: '#06b6d4',
      [EntityType.AIRCRAFT]: '#f59e0b',
      [EntityType.FACILITY]: '#ef4444',
      [EntityType.EQUIPMENT]: '#9ca3af',
      [EntityType.UNIT]: '#10b981',
      [EntityType.SIGNAL]: '#8b5cf6',
      [EntityType.CYBER]: '#8b5cf6',
      [EntityType.UNKNOWN]: '#6b7280',
    };

    const base = colorMap[entityType] ?? '#6b7280';
    return {
      background: base,
      border: base,
      highlight: {
        background: base,
        border: '#ffffff',
      },
    };
  }

  private getEdgeColor(linkType: string): { color: string; highlight: string; opacity: number } {
    const colorMap: Record<string, string> = {
      [LinkType.ASSOCIATED]: '#6b7280',
      [LinkType.COMMUNICATION]: '#3b82f6',
      [LinkType.FINANCIAL]: '#f59e0b',
      [LinkType.ORGANIZATIONAL]: '#10b981',
      [LinkType.GEOGRAPHIC]: '#06b6d4',
      [LinkType.FAMILIAL]: '#ec4899',
      [LinkType.LOGISTIC]: '#8b5cf6',
      [LinkType.OPERATIONAL]: '#ef4444',
      [LinkType.IDENTITY]: '#f97316',
    };

    const color = colorMap[linkType] ?? '#4b5563';
    return {
      color,
      highlight: '#ffffff',
      opacity: 0.7,
    };
  }
}
