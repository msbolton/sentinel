import { LinkType } from '@sentinel/proto-gen';
export { LinkType };

export interface Link {
  id: string;
  sourceEntityId: string;
  targetEntityId: string;
  linkType: LinkType;
  confidence: number;
  description?: string;
  metadata: Record<string, string>;
  createdAt: string;
  updatedAt: string;
}

export interface GraphNode {
  id: string;
  label: string;
  entityType: string;
  group: string;
  title?: string;
  color?: string | { background: string; border: string; highlight: { background: string; border: string } };
  size?: number;
  shape?: string;
  font?: { color: string };
}

export interface GraphEdge {
  id: string;
  from: string;
  to: string;
  label: string;
  title?: string;
  color?: string | { color: string; highlight: string; opacity: number };
  width?: number;
  dashes?: boolean;
  arrows?: string;
  font?: { color: string; size: number; align: string };
  value?: number;
}

export interface GraphData {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

export interface GraphQuery {
  entityId: string;
  depth?: number;
  linkTypes?: LinkType[];
}
