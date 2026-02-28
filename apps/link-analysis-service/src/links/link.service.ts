import {
  Injectable,
  Logger,
  NotFoundException,
  InternalServerErrorException,
} from '@nestjs/common';
import { InjectRepository } from '@nestjs/typeorm';
import { Repository, In } from 'typeorm';
import { LinkRecord } from './link.entity';
import { LinkType } from './link-type.enum';
import { AgeService } from './age.service';
import { CreateLinkDto } from './dto/create-link.dto';

export interface GraphNode {
  id: string;
  entityId: string;
  label: string;
  properties: Record<string, unknown>;
}

export interface GraphEdge {
  id: string;
  sourceEntityId: string;
  targetEntityId: string;
  linkType: string;
  confidence: number;
  properties: Record<string, unknown>;
}

export interface GraphResult {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

@Injectable()
export class LinkService {
  private readonly logger = new Logger(LinkService.name);

  constructor(
    @InjectRepository(LinkRecord)
    private readonly linkRepo: Repository<LinkRecord>,
    private readonly ageService: AgeService,
  ) {}

  /**
   * Create a link in both the PostgreSQL table and the Apache AGE graph.
   */
  async createLink(dto: CreateLinkDto): Promise<LinkRecord> {
    // Insert into PostgreSQL relational table
    const link = this.linkRepo.create({
      sourceEntityId: dto.sourceEntityId,
      targetEntityId: dto.targetEntityId,
      linkType: dto.linkType,
      confidence: dto.confidence ?? 0.5,
      description: dto.description,
      evidence: dto.evidence || [],
      firstObserved: dto.firstObserved ? new Date(dto.firstObserved) : undefined,
      lastObserved: dto.lastObserved ? new Date(dto.lastObserved) : undefined,
      metadata: dto.metadata || {},
    });

    const savedLink = await this.linkRepo.save(link);

    // Sync to Apache AGE graph
    try {
      await this.syncLinkToGraph(savedLink);
    } catch (error) {
      this.logger.warn(
        `Failed to sync link ${savedLink.id} to graph - relational record saved`,
        error instanceof Error ? error.message : String(error),
      );
    }

    return savedLink;
  }

  /**
   * Query links for a given entity with optional filters.
   */
  async getLinks(
    entityId: string,
    linkTypes?: LinkType[],
    minConfidence?: number,
  ): Promise<LinkRecord[]> {
    const queryBuilder = this.linkRepo
      .createQueryBuilder('link')
      .where(
        '(link."sourceEntityId" = :entityId OR link."targetEntityId" = :entityId)',
        { entityId },
      );

    if (linkTypes && linkTypes.length > 0) {
      queryBuilder.andWhere('link."linkType" IN (:...linkTypes)', {
        linkTypes,
      });
    }

    if (minConfidence !== undefined) {
      queryBuilder.andWhere('link.confidence >= :minConfidence', {
        minConfidence,
      });
    }

    queryBuilder.orderBy('link.confidence', 'DESC');

    return queryBuilder.getMany();
  }

  /**
   * Traverse the Apache AGE graph from a center entity using Cypher.
   * Returns all connected entities within maxDepth hops.
   */
  async getGraph(
    centerEntityId: string,
    maxDepth: number = 3,
    linkTypes?: LinkType[],
    minConfidence?: number,
  ): Promise<GraphResult> {
    try {
      let whereClause = '';
      const conditions: string[] = [];

      if (minConfidence !== undefined) {
        conditions.push(
          `ALL(rel IN relationships(path) WHERE rel.confidence >= ${minConfidence})`,
        );
      }

      if (linkTypes && linkTypes.length > 0) {
        const typesStr = linkTypes.map((t) => `'${t}'`).join(', ');
        conditions.push(
          `ALL(rel IN relationships(path) WHERE rel.link_type IN [${typesStr}])`,
        );
      }

      if (conditions.length > 0) {
        whereClause = `WHERE ${conditions.join(' AND ')}`;
      }

      const cypher = `
        MATCH path = (start:Entity {entity_id: '${this.sanitizeUuid(centerEntityId)}'})-[r*1..${maxDepth}]-(connected:Entity)
        ${whereClause}
        RETURN path
      `;

      const results = await this.ageService.executeCypher(
        cypher,
        '(path agtype)',
      );

      return this.parseGraphResults(results);
    } catch (error) {
      this.logger.error(
        `Graph query failed for entity ${centerEntityId}`,
        error instanceof Error ? error.stack : String(error),
      );

      // Fallback to relational query
      return this.getGraphFromRelational(
        centerEntityId,
        maxDepth,
        linkTypes,
        minConfidence,
      );
    }
  }

  /**
   * Find shortest path between two entities using Cypher.
   */
  async findShortestPath(
    entityId1: string,
    entityId2: string,
  ): Promise<GraphResult> {
    try {
      const cypher = `
        MATCH path = shortestPath(
          (a:Entity {entity_id: '${this.sanitizeUuid(entityId1)}'})-[*]-(b:Entity {entity_id: '${this.sanitizeUuid(entityId2)}'})
        )
        RETURN path
      `;

      const results = await this.ageService.executeCypher(
        cypher,
        '(path agtype)',
      );

      if (results.length === 0) {
        return { nodes: [], edges: [] };
      }

      return this.parseGraphResults(results);
    } catch (error) {
      this.logger.error(
        `Shortest path query failed between ${entityId1} and ${entityId2}`,
        error instanceof Error ? error.stack : String(error),
      );
      throw new InternalServerErrorException('Shortest path query failed');
    }
  }

  /**
   * Run community detection pattern using Cypher.
   * Uses a simple label propagation-like approach via connected component analysis.
   */
  async detectCommunities(): Promise<
    { communityId: number; entityIds: string[] }[]
  > {
    try {
      const cypher = `
        MATCH (n:Entity)
        WITH collect(n) AS nodes
        UNWIND nodes AS node
        MATCH path = (node)-[:ASSOCIATION|COMMUNICATION|ORGANIZATIONAL*1..3]-(connected:Entity)
        WITH node.entity_id AS centerId, collect(DISTINCT connected.entity_id) AS members
        RETURN centerId, members
      `;

      const results = await this.ageService.executeCypher(
        cypher,
        '(centerId agtype, members agtype)',
      );

      // Group into communities by merging overlapping member sets
      const communities: { communityId: number; entityIds: string[] }[] = [];
      const assigned = new Set<string>();
      let communityId = 0;

      for (const row of results as { centerId: string; members: string[] }[]) {
        const center = String(row.centerId).replace(/"/g, '');
        if (!assigned.has(center)) {
          const members = Array.isArray(row.members)
            ? row.members.map((m) => String(m).replace(/"/g, ''))
            : [center];
          members.push(center);

          const uniqueMembers = [...new Set(members)].filter(
            (m) => !assigned.has(m),
          );
          if (uniqueMembers.length > 0) {
            communities.push({ communityId, entityIds: uniqueMembers });
            uniqueMembers.forEach((m) => assigned.add(m));
            communityId++;
          }
        }
      }

      return communities;
    } catch (error) {
      this.logger.error(
        'Community detection failed',
        error instanceof Error ? error.stack : String(error),
      );
      throw new InternalServerErrorException('Community detection failed');
    }
  }

  /**
   * Create or update a vertex in the Apache AGE graph for an entity.
   */
  async syncToGraph(entity: {
    id: string;
    name: string;
    entityType: string;
  }): Promise<void> {
    try {
      // Try to update first
      const updateResult = await this.ageService.updateVertex(
        'Entity',
        'entity_id',
        entity.id,
        {
          name: entity.name,
          entity_type: entity.entityType,
        },
      );

      if (!updateResult) {
        // Vertex doesn't exist; create it
        await this.ageService.createVertex('Entity', {
          entity_id: entity.id,
          name: entity.name,
          entity_type: entity.entityType,
        });
      }
    } catch (error) {
      // If update failed because vertex doesn't exist, create it
      try {
        await this.ageService.createVertex('Entity', {
          entity_id: entity.id,
          name: entity.name,
          entity_type: entity.entityType,
        });
      } catch (createError) {
        this.logger.error(
          `Failed to sync entity ${entity.id} to graph`,
          createError instanceof Error ? createError.stack : String(createError),
        );
      }
    }
  }

  /**
   * Create or update an edge in the Apache AGE graph for a link.
   */
  async syncLinkToGraph(link: LinkRecord): Promise<void> {
    try {
      await this.ageService.createEdge(
        link.linkType,
        'Entity',
        'entity_id',
        link.sourceEntityId,
        'Entity',
        'entity_id',
        link.targetEntityId,
        {
          link_id: link.id,
          link_type: link.linkType,
          confidence: link.confidence,
          description: link.description || '',
        },
      );
    } catch (error) {
      this.logger.error(
        `Failed to sync link ${link.id} to graph`,
        error instanceof Error ? error.stack : String(error),
      );
    }
  }

  /**
   * Delete a link by ID from both PostgreSQL and the graph.
   */
  async deleteLink(id: string): Promise<void> {
    const link = await this.linkRepo.findOne({ where: { id } });
    if (!link) {
      throw new NotFoundException(`Link ${id} not found`);
    }

    await this.linkRepo.remove(link);

    // Remove from graph
    try {
      await this.ageService.deleteEdge(link.linkType, 'link_id', id);
    } catch (error) {
      this.logger.warn(
        `Failed to remove link ${id} from graph`,
        error instanceof Error ? error.message : String(error),
      );
    }
  }

  /**
   * Sanitize a UUID string for safe interpolation into Cypher queries.
   * Strips any characters that are not hex digits or dashes.
   */
  private sanitizeUuid(value: string): string {
    return value.replace(/[^a-fA-F0-9-]/g, '');
  }

  /**
   * Parse AGE graph results into a structured GraphResult with nodes and edges.
   */
  private parseGraphResults(results: unknown[]): GraphResult {
    const nodesMap = new Map<string, GraphNode>();
    const edgesMap = new Map<string, GraphEdge>();

    for (const row of results) {
      // AGE returns agtype data; parse as needed
      const pathData = row as Record<string, unknown>;
      const path = pathData['path'] || pathData;

      // Extract nodes and relationships from the path
      // AGE path format varies; handle common structures
      if (typeof path === 'string') {
        try {
          const parsed = JSON.parse(path);
          this.extractFromParsedPath(parsed, nodesMap, edgesMap);
        } catch {
          this.logger.debug(`Could not parse path: ${path}`);
        }
      }
    }

    return {
      nodes: Array.from(nodesMap.values()),
      edges: Array.from(edgesMap.values()),
    };
  }

  /**
   * Extract nodes and edges from a parsed AGE path object.
   */
  private extractFromParsedPath(
    parsed: unknown,
    nodesMap: Map<string, GraphNode>,
    edgesMap: Map<string, GraphEdge>,
  ): void {
    if (Array.isArray(parsed)) {
      for (const element of parsed) {
        this.extractFromParsedPath(element, nodesMap, edgesMap);
      }
    } else if (typeof parsed === 'object' && parsed !== null) {
      const obj = parsed as Record<string, unknown>;
      if (obj['id'] && obj['label'] && obj['properties']) {
        const props = obj['properties'] as Record<string, unknown>;
        if (obj['start_id'] && obj['end_id']) {
          // Edge
          const edgeId = String(obj['id']);
          if (!edgesMap.has(edgeId)) {
            edgesMap.set(edgeId, {
              id: edgeId,
              sourceEntityId: String(props['source_entity_id'] || ''),
              targetEntityId: String(props['target_entity_id'] || ''),
              linkType: String(props['link_type'] || obj['label']),
              confidence: Number(props['confidence'] || 0.5),
              properties: props,
            });
          }
        } else {
          // Node
          const nodeId = String(obj['id']);
          if (!nodesMap.has(nodeId)) {
            nodesMap.set(nodeId, {
              id: nodeId,
              entityId: String(props['entity_id'] || ''),
              label: String(obj['label']),
              properties: props,
            });
          }
        }
      }
    }
  }

  /**
   * Fallback: get graph structure from relational data when AGE is unavailable.
   */
  private async getGraphFromRelational(
    centerEntityId: string,
    maxDepth: number,
    linkTypes?: LinkType[],
    minConfidence?: number,
  ): Promise<GraphResult> {
    const visited = new Set<string>();
    const nodes: GraphNode[] = [];
    const edges: GraphEdge[] = [];
    const queue: { entityId: string; depth: number }[] = [
      { entityId: centerEntityId, depth: 0 },
    ];

    while (queue.length > 0) {
      const current = queue.shift()!;
      if (visited.has(current.entityId) || current.depth > maxDepth) {
        continue;
      }
      visited.add(current.entityId);

      nodes.push({
        id: current.entityId,
        entityId: current.entityId,
        label: 'Entity',
        properties: {},
      });

      const links = await this.getLinks(
        current.entityId,
        linkTypes,
        minConfidence,
      );

      for (const link of links) {
        edges.push({
          id: link.id,
          sourceEntityId: link.sourceEntityId,
          targetEntityId: link.targetEntityId,
          linkType: link.linkType,
          confidence: link.confidence,
          properties: { description: link.description },
        });

        const connectedId =
          link.sourceEntityId === current.entityId
            ? link.targetEntityId
            : link.sourceEntityId;

        if (!visited.has(connectedId)) {
          queue.push({ entityId: connectedId, depth: current.depth + 1 });
        }
      }
    }

    return { nodes, edges };
  }
}
