import { Injectable, Logger, OnModuleInit } from '@nestjs/common';
import { InjectDataSource } from '@nestjs/typeorm';
import { DataSource } from 'typeorm';

/**
 * Low-level Apache AGE query service for graph operations.
 * Apache AGE is a PostgreSQL extension that provides graph database
 * functionality using Cypher query language.
 */
@Injectable()
export class AgeService implements OnModuleInit {
  private readonly logger = new Logger(AgeService.name);
  private static readonly GRAPH_NAME = 'entity_graph';

  constructor(
    @InjectDataSource()
    private readonly dataSource: DataSource,
  ) {}

  async onModuleInit(): Promise<void> {
    await this.ensureGraphExists();
  }

  /**
   * Ensure the AGE extension is loaded and the graph exists.
   */
  private async ensureGraphExists(): Promise<void> {
    try {
      // Load AGE extension
      await this.dataSource.query(`LOAD 'age'`);
      await this.dataSource.query(
        `SET search_path = ag_catalog, "$user", public`,
      );

      // Create graph if it doesn't exist
      const result = await this.dataSource.query(
        `SELECT * FROM ag_catalog.ag_graph WHERE name = $1`,
        [AgeService.GRAPH_NAME],
      );

      if (result.length === 0) {
        await this.dataSource.query(
          `SELECT create_graph($1)`,
          [AgeService.GRAPH_NAME],
        );
        this.logger.log(`Created graph: ${AgeService.GRAPH_NAME}`);
      } else {
        this.logger.log(`Graph ${AgeService.GRAPH_NAME} already exists`);
      }
    } catch (error) {
      this.logger.warn(
        'Apache AGE initialization failed - graph operations will not be available',
        error instanceof Error ? error.message : String(error),
      );
    }
  }

  /**
   * Execute a Cypher query via Apache AGE SQL wrapper.
   */
  async executeCypher<T = unknown>(
    cypher: string,
    returnType: string = '(result agtype)',
  ): Promise<T[]> {
    const sql = `
      SELECT * FROM cypher('${AgeService.GRAPH_NAME}', $$
        ${cypher}
      $$) as ${returnType};
    `;

    try {
      await this.loadAge();
      return await this.dataSource.query(sql);
    } catch (error) {
      this.logger.error(
        `Cypher query failed: ${cypher}`,
        error instanceof Error ? error.stack : String(error),
      );
      throw error;
    }
  }

  /**
   * Create a vertex (node) in the graph.
   */
  async createVertex(
    label: string,
    properties: Record<string, unknown>,
  ): Promise<unknown> {
    const propsString = this.formatProperties(properties);
    const cypher = `CREATE (n:${this.escapeLabel(label)} ${propsString}) RETURN n`;

    const results = await this.executeCypher(cypher, '(n agtype)');
    return results[0];
  }

  /**
   * Update a vertex's properties. Merges with existing properties.
   */
  async updateVertex(
    label: string,
    matchProperty: string,
    matchValue: string,
    properties: Record<string, unknown>,
  ): Promise<unknown> {
    const setClause = Object.entries(properties)
      .map(([key, value]) => `n.${key} = ${this.formatValue(value)}`)
      .join(', ');

    const cypher = `
      MATCH (n:${this.escapeLabel(label)} {${matchProperty}: ${this.formatValue(matchValue)}})
      SET ${setClause}
      RETURN n
    `;

    const results = await this.executeCypher(cypher, '(n agtype)');
    return results[0];
  }

  /**
   * Create an edge (relationship) between two vertices.
   */
  async createEdge(
    label: string,
    fromLabel: string,
    fromMatchProp: string,
    fromMatchValue: string,
    toLabel: string,
    toMatchProp: string,
    toMatchValue: string,
    properties: Record<string, unknown>,
  ): Promise<unknown> {
    const propsString = this.formatProperties(properties);

    const cypher = `
      MATCH (a:${this.escapeLabel(fromLabel)} {${fromMatchProp}: ${this.formatValue(fromMatchValue)}}),
            (b:${this.escapeLabel(toLabel)} {${toMatchProp}: ${this.formatValue(toMatchValue)}})
      CREATE (a)-[r:${this.escapeLabel(label)} ${propsString}]->(b)
      RETURN r
    `;

    const results = await this.executeCypher(cypher, '(r agtype)');
    return results[0];
  }

  /**
   * Delete a vertex from the graph by a matching property.
   */
  async deleteVertex(
    label: string,
    matchProperty: string,
    matchValue: string,
  ): Promise<void> {
    const cypher = `
      MATCH (n:${this.escapeLabel(label)} {${matchProperty}: ${this.formatValue(matchValue)}})
      DETACH DELETE n
    `;

    await this.executeCypher(cypher, '(result agtype)');
  }

  /**
   * Delete an edge from the graph by matching its link_id property.
   */
  async deleteEdge(
    label: string,
    matchProperty: string,
    matchValue: string,
  ): Promise<void> {
    const cypher = `
      MATCH ()-[r:${this.escapeLabel(label)} {${matchProperty}: ${this.formatValue(matchValue)}}]-()
      DELETE r
    `;

    await this.executeCypher(cypher, '(result agtype)');
  }

  /**
   * Load the AGE extension for the current session.
   */
  private async loadAge(): Promise<void> {
    await this.dataSource.query(`LOAD 'age'`);
    await this.dataSource.query(
      `SET search_path = ag_catalog, "$user", public`,
    );
  }

  /**
   * Format a properties object into Cypher map syntax: {key1: 'val1', key2: 123}
   */
  private formatProperties(properties: Record<string, unknown>): string {
    const entries = Object.entries(properties)
      .map(([key, value]) => `${key}: ${this.formatValue(value)}`)
      .join(', ');

    return `{${entries}}`;
  }

  /**
   * Format a single value for Cypher syntax.
   */
  private formatValue(value: unknown): string {
    if (value === null || value === undefined) {
      return 'null';
    }
    if (typeof value === 'string') {
      // Escape single quotes in strings
      return `'${value.replace(/'/g, "\\'")}'`;
    }
    if (typeof value === 'number') {
      return String(value);
    }
    if (typeof value === 'boolean') {
      return value ? 'true' : 'false';
    }
    if (Array.isArray(value)) {
      return `[${value.map((v) => this.formatValue(v)).join(', ')}]`;
    }
    // Fallback: serialize as JSON string
    return `'${JSON.stringify(value).replace(/'/g, "\\'")}'`;
  }

  /**
   * Escape a label name for safe use in Cypher.
   */
  private escapeLabel(label: string): string {
    // Only allow alphanumeric and underscore
    return label.replace(/[^a-zA-Z0-9_]/g, '_');
  }
}
