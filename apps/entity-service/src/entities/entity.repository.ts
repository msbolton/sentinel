import { Injectable } from '@nestjs/common';
import { DataSource, Repository, SelectQueryBuilder } from 'typeorm';
import { EntityRecord } from './entity.entity';
import { Classification, CLASSIFICATION_ORDER, EntitySource, EntityType } from './enums';

export interface BoundingBoxParams {
  north: number;
  south: number;
  east: number;
  west: number;
  entityTypes?: EntityType[];
  sources?: EntitySource[];
  maxClassification?: Classification;
  limit: number;
  offset: number;
}

export interface NearbyParams {
  lat: number;
  lng: number;
  radiusMeters: number;
}

export interface EntityWithDistance extends EntityRecord {
  distance: number;
}

export interface EntityCountByType {
  entityType: EntityType;
  classification: Classification;
  count: number;
}

export interface ExistingEntityInfo {
  id: string;
  name: string;
  entityType: EntityType;
  classification: Classification;
  source: EntitySource;
  metadata: Record<string, unknown>;
  sourceEntityId: string | null;
  ageoutState: string;
}

@Injectable()
export class EntityRepository extends Repository<EntityRecord> {
  constructor(private readonly dataSource: DataSource) {
    super(EntityRecord, dataSource.createEntityManager());
  }

  /**
   * Find all non-deleted entities within a geographic bounding box,
   * optionally filtered by entity type, source, and classification ceiling.
   */
  async findWithinBoundingBox(params: BoundingBoxParams): Promise<[EntityRecord[], number]> {
    const {
      north,
      south,
      east,
      west,
      entityTypes,
      sources,
      maxClassification,
      limit,
      offset,
    } = params;

    const qb = this.createQueryBuilder('e')
      .where('e.deleted = :deleted', { deleted: false })
      .andWhere('e.position IS NOT NULL')
      .andWhere('e.ageoutState != :agedOut', { agedOut: 'AGED_OUT' })
      .andWhere(
        'ST_Within(e.position, ST_MakeEnvelope(:west, :south, :east, :north, 4326))',
        { west, south, east, north },
      );

    this.applyFilters(qb, entityTypes, sources, maxClassification);

    qb.orderBy('e.lastSeenAt', 'DESC', 'NULLS LAST')
      .skip(offset)
      .take(limit);

    return qb.getManyAndCount();
  }

  /**
   * Find all non-deleted entities within a given radius (meters) of a point,
   * ordered by ascending distance. Uses PostGIS geography cast for accurate
   * great-circle distance on the WGS84 ellipsoid.
   */
  async findNearby(params: NearbyParams): Promise<EntityWithDistance[]> {
    const { lat, lng, radiusMeters } = params;

    const results = await this.createQueryBuilder('e')
      .addSelect(
        `ST_Distance(
          e.position::geography,
          ST_SetSRID(ST_MakePoint(:lng, :lat), 4326)::geography
        )`,
        'distance',
      )
      .where('e.deleted = :deleted', { deleted: false })
      .andWhere('e.ageoutState != :agedOut', { agedOut: 'AGED_OUT' })
      .andWhere('e.position IS NOT NULL')
      .andWhere(
        `ST_DWithin(
          e.position::geography,
          ST_SetSRID(ST_MakePoint(:lng, :lat), 4326)::geography,
          :radiusMeters
        )`,
        { lng, lat, radiusMeters },
      )
      .orderBy('distance', 'ASC')
      .setParameters({ lng, lat, radiusMeters })
      .getRawAndEntities();

    // Merge the computed distance into each entity
    return results.entities.map((entity, index) => ({
      ...entity,
      distance: parseFloat(results.raw[index]['distance']),
    }));
  }

  /**
   * Update only the spatial position and kinematic fields for a given entity.
   * Also bumps `lastSeenAt` to the current timestamp.
   */
  async updatePosition(
    id: string,
    lat: number,
    lng: number,
    heading?: number,
    speedKnots?: number,
    course?: number,
    altitude?: number,
  ): Promise<EntityRecord | null> {
    const result = await this.query(
      `UPDATE sentinel.entities
       SET position = ST_SetSRID(ST_MakePoint($1, $2), 4326),
           heading = $3, "speedKnots" = $4, course = $5, altitude = $6,
           "lastSeenAt" = NOW()
       WHERE id = $7 AND deleted = false
       RETURNING *`,
      [lng, lat, heading ?? null, speedKnots ?? null, course ?? null, altitude ?? null, id],
    );

    if (!result || result.length === 0) return null;

    return this.mapRawToEntity(result[0]);
  }

  private mapRawToEntity(raw: Record<string, unknown>): EntityRecord {
    return this.create({
      id: raw.id as string,
      entityType: raw.entityType as EntityType,
      name: raw.name as string,
      description: raw.description as string,
      source: raw.source as EntitySource,
      classification: raw.classification as Classification,
      position: raw.position as object | null,
      heading: raw.heading as number | null,
      speedKnots: raw.speedKnots as number | null,
      course: raw.course as number | null,
      altitude: raw.altitude as number | null,
      milStd2525dSymbol: raw.milStd2525dSymbol as string | null,
      metadata: raw.metadata as Record<string, unknown>,
      affiliations: raw.affiliations as string[],
      affiliation: raw.affiliation as string,
      identityConfidence: raw.identityConfidence as number,
      characterization: raw.characterization as string,
      trackEnvironment: raw.trackEnvironment as string,
      trackProcessingState: raw.trackProcessingState as string,
      pitch: raw.pitch as number | null,
      roll: raw.roll as number | null,
      operationalStatus: raw.operationalStatus as string,
      damageAssessment: raw.damageAssessment as string,
      damageConfidence: raw.damageConfidence as number,
      dimensionLength: raw.dimensionLength as number | null,
      dimensionWidth: raw.dimensionWidth as number | null,
      dimensionHeight: raw.dimensionHeight as number | null,
      countryOfOrigin: raw.countryOfOrigin as string | null,
      kinematics: raw.kinematics as Record<string, unknown>,
      platformData: raw.platformData as Record<string, unknown>,
      circularError: raw.circularError as number | null,
      lastObservationSource: raw.lastObservationSource as string | null,
      sourceEntityId: raw.sourceEntityId as string | null,
      createdAt: raw.createdAt as Date,
      updatedAt: raw.updatedAt as Date,
      lastSeenAt: raw.lastSeenAt as Date | null,
      deleted: raw.deleted as boolean,
      deletedAt: raw.deletedAt as Date | null,
    });
  }

  /**
   * Bulk lookup of entities by their sourceEntityId metadata field.
   */
  async findBySourceEntityIds(
    sourceEntityIds: string[],
  ): Promise<Map<string, ExistingEntityInfo>> {
    if (sourceEntityIds.length === 0) return new Map();

    const results = await this.createQueryBuilder('e')
      .select([
        'e.id',
        'e.name',
        'e.entityType',
        'e.classification',
        'e.source',
        'e.metadata',
        'e.sourceEntityId',
        'e.ageoutState',
      ])
      .where('e.deleted = :deleted', { deleted: false })
      .andWhere('e.sourceEntityId IN (:...sourceEntityIds)', { sourceEntityIds })
      .getRawMany();

    const map = new Map<string, ExistingEntityInfo>();
    for (const row of results) {
      map.set(row.e_sourceEntityId, {
        id: row.e_id,
        name: row.e_name,
        entityType: row.e_entityType,
        classification: row.e_classification,
        source: row.e_source,
        metadata: row.e_metadata,
        sourceEntityId: row.e_sourceEntityId,
        ageoutState: row.e_ageoutState,
      });
    }
    return map;
  }

  /**
   * Bulk update positions for multiple entities in a single SQL statement.
   */
  async bulkUpdatePositions(
    updates: Array<{
      id: string;
      lat: number;
      lng: number;
      heading: number | null;
      speedKnots: number | null;
      course: number | null;
      altitude: number | null;
      platformData?: Record<string, unknown> | null;
      kinematics?: Record<string, unknown> | null;
      trackEnvironment?: string | null;
      circularError?: number | null;
    }>,
  ): Promise<void> {
    if (updates.length === 0) return;

    const COLS_PER_ROW = 11;
    const params: unknown[] = [];
    const valueClauses: string[] = [];
    for (let i = 0; i < updates.length; i++) {
      const u = updates[i];
      const offset = i * COLS_PER_ROW;
      valueClauses.push(
        `($${offset + 1}::uuid, $${offset + 2}::double precision, $${offset + 3}::double precision, $${offset + 4}::double precision, $${offset + 5}::double precision, $${offset + 6}::double precision, $${offset + 7}::double precision, $${offset + 8}::jsonb, $${offset + 9}::jsonb, $${offset + 10}::varchar, $${offset + 11}::double precision)`,
      );
      params.push(
        u.id, u.lng, u.lat, u.heading, u.speedKnots, u.course, u.altitude,
        u.platformData ? JSON.stringify(u.platformData) : null,
        u.kinematics ? JSON.stringify(u.kinematics) : null,
        u.trackEnvironment ?? null,
        u.circularError ?? null,
      );
    }

    await this.query(
      `UPDATE sentinel.entities AS e
       SET position = ST_SetSRID(ST_MakePoint(b.lng, b.lat), 4326),
           heading = b.heading,
           "speedKnots" = b.speed_knots,
           course = b.course,
           altitude = b.altitude,
           "platformData" = COALESCE(b.platform_data, e."platformData"),
           kinematics = COALESCE(b.kinematics, e.kinematics),
           "trackEnvironment" = COALESCE(b.track_env, e."trackEnvironment"),
           "circularError" = COALESCE(b.circular_err, e."circularError"),
           "lastSeenAt" = NOW(),
           "ageoutState" = 'LIVE'
       FROM (VALUES ${valueClauses.join(',')}) AS b(id, lng, lat, heading, speed_knots, course, altitude, platform_data, kinematics, track_env, circular_err)
       WHERE e.id = b.id AND e.deleted = false`,
      params,
    );
  }

  /**
   * Soft-delete an entity by marking it as deleted.
   */
  async softDeleteEntity(id: string): Promise<boolean> {
    const result = await this.createQueryBuilder()
      .update(EntityRecord)
      .set({ deleted: true, deletedAt: new Date() })
      .where('id = :id AND deleted = :deleted', { id, deleted: false })
      .execute();

    return (result.affected ?? 0) > 0;
  }

  /**
   * Soft-delete ALL active entities in a single bulk UPDATE.
   * Returns the number of affected rows.
   */
  async softDeleteAll(): Promise<number> {
    const result = await this.createQueryBuilder()
      .update(EntityRecord)
      .set({
        deleted: true,
        deletedAt: new Date(),
        ageoutState: 'AGED_OUT' as any,
      })
      .where('deleted = :deleted', { deleted: false })
      .execute();

    return result.affected ?? 0;
  }

  /**
   * Get aggregate counts of entities grouped by type and classification.
   */
  async getEntityCounts(): Promise<EntityCountByType[]> {
    const raw = await this.createQueryBuilder('e')
      .select('e.entityType', 'entityType')
      .addSelect('e.classification', 'classification')
      .addSelect('COUNT(*)::int', 'count')
      .where('e.deleted = :deleted', { deleted: false })
      .groupBy('e.entityType')
      .addGroupBy('e.classification')
      .getRawMany<{ entityType: EntityType; classification: Classification; count: string }>();

    return raw.map((row) => ({
      entityType: row.entityType,
      classification: row.classification,
      count: parseInt(row.count, 10),
    }));
  }

  /**
   * Find a single non-deleted entity by ID.
   */
  async findActiveById(id: string): Promise<EntityRecord | null> {
    return this.findOneBy({ id, deleted: false });
  }

  // ─── Private helpers ────────────────────────────────────────────────

  private applyFilters(
    qb: SelectQueryBuilder<EntityRecord>,
    entityTypes?: EntityType[],
    sources?: EntitySource[],
    maxClassification?: Classification,
  ): void {
    if (entityTypes && entityTypes.length > 0) {
      qb.andWhere('e.entityType IN (:...entityTypes)', { entityTypes });
    }

    if (sources && sources.length > 0) {
      qb.andWhere('e.source IN (:...sources)', { sources });
    }

    if (maxClassification) {
      const allowedLevels = CLASSIFICATION_ORDER.slice(
        0,
        CLASSIFICATION_ORDER.indexOf(maxClassification) + 1,
      );
      qb.andWhere('e.classification IN (:...allowedLevels)', { allowedLevels });
    }
  }
}
