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
    await this.createQueryBuilder()
      .update(EntityRecord)
      .set({
        position: () => `ST_SetSRID(ST_MakePoint(${lng}, ${lat}), 4326)`,
        heading: heading ?? null,
        speedKnots: speedKnots ?? null,
        course: course ?? null,
        altitude: altitude ?? null,
        lastSeenAt: new Date(),
      })
      .where('id = :id AND deleted = :deleted', { id, deleted: false })
      .execute();

    return this.findOneBy({ id, deleted: false });
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
