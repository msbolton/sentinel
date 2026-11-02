import { EntityType } from '../../shared/models/entity.model';
import { GraphEdge, GraphNode, GraphResult, LinkType } from '../../shared/models/link.model';

/** What vis-network draws for one entity. */
export interface VisNode {
  id: string;
  label: string;
  title: string;
  color: { background: string; border: string; highlight: { background: string; border: string } };
  shape: 'dot';
  size: number;
  borderWidth: number;
  font: { color: string; size: number; face: string };
}

/** What vis-network draws for one link. */
export interface VisEdge {
  id: string;
  from: string;
  to: string;
  label: string;
  title: string;
  width: number;
  color: { color: string; highlight: string; opacity: number };
  arrows: { to: { enabled: boolean; scaleFactor: number } };
  font: { color: string; size: number; face: string; align: 'middle'; strokeWidth: number; strokeColor: string };
  smooth: { type: 'continuous'; roundness: number };
}

const NODE_COLORS: Record<string, string> = {
  [EntityType.PERSON]: '#3b82f6',
  [EntityType.VEHICLE]: '#10b981',
  [EntityType.VESSEL]: '#06b6d4',
  [EntityType.AIRCRAFT]: '#f59e0b',
  [EntityType.FACILITY]: '#ef4444',
  [EntityType.EQUIPMENT]: '#9ca3af',
  [EntityType.UNIT]: '#10b981',
  [EntityType.SIGNAL]: '#8b5cf6',
  [EntityType.CYBER]: '#8b5cf6',
  [EntityType.UNKNOWN]: '#5a6a80',
};

const EDGE_COLORS: Record<string, string> = {
  [LinkType.ASSOCIATION]: '#6b7280',
  [LinkType.COMMUNICATION]: '#3b82f6',
  [LinkType.COLOCATION]: '#14b8a6',
  [LinkType.FINANCIAL]: '#f59e0b',
  [LinkType.ORGANIZATIONAL]: '#10b981',
  [LinkType.MOVEMENT_PATTERN]: '#a3e635',
  [LinkType.COMMAND_CONTROL]: '#dc2626',
  [LinkType.GEOGRAPHIC]: '#06b6d4',
  [LinkType.FAMILIAL]: '#ec4899',
  [LinkType.LOGISTIC]: '#8b5cf6',
  [LinkType.OPERATIONAL]: '#ef4444',
  [LinkType.IDENTITY]: '#f97316',
};

export function nodeColor(entityType: string): string {
  return NODE_COLORS[entityType] ?? '#5a6a80';
}

export function edgeColor(linkType: string): string {
  return EDGE_COLORS[linkType] ?? '#4b5563';
}

/** A link type as a label: COMMAND_CONTROL reads "command control". */
export function linkTypeLabel(linkType: string): string {
  return linkType.toLowerCase().replace(/_/g, ' ');
}

/**
 * Converts the service's graph into vis-network's nodes and edges. The
 * center is drawn larger; an edge is as wide as the link is confident.
 */
export function toVisGraph(graph: GraphResult, centerId: string, fontFace: string): { nodes: VisNode[]; edges: VisEdge[] } {
  return {
    nodes: graph.nodes.map((n) => toVisNode(n, n.entityId === centerId, fontFace)),
    edges: graph.edges.map((e) => toVisEdge(e, fontFace)),
  };
}

function toVisNode(node: GraphNode, isCenter: boolean, fontFace: string): VisNode {
  const base = nodeColor(node.entityType);
  return {
    id: node.entityId,
    label: node.name,
    title: `${node.name} (${node.entityType})`,
    color: { background: base, border: isCenter ? '#ffffff' : base, highlight: { background: base, border: '#ffffff' } },
    shape: 'dot',
    size: isCenter ? 22 : 16,
    borderWidth: isCenter ? 3 : 2,
    font: { color: '#e0e6ed', size: 12, face: fontFace },
  };
}

function toVisEdge(edge: GraphEdge, fontFace: string): VisEdge {
  return {
    id: edge.id,
    from: edge.sourceEntityId,
    to: edge.targetEntityId,
    label: linkTypeLabel(edge.linkType),
    title: `${linkTypeLabel(edge.linkType)} · ${Math.round(edge.confidence * 100)}%`,
    width: 1 + edge.confidence * 3,
    color: { color: edgeColor(edge.linkType), highlight: '#ffffff', opacity: 0.7 },
    arrows: { to: { enabled: true, scaleFactor: 0.5 } },
    font: { color: '#5a6a80', size: 10, face: fontFace, align: 'middle', strokeWidth: 3, strokeColor: '#0a0e17' },
    smooth: { type: 'continuous', roundness: 0.2 },
  };
}
