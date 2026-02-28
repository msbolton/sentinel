import {
  Controller,
  Get,
  Post,
  Patch,
  Delete,
  Param,
  Body,
  Query,
  HttpCode,
  HttpStatus,
  ParseUUIDPipe,
  Logger,
} from '@nestjs/common';
import {
  ApiTags,
  ApiOperation,
  ApiResponse,
  ApiParam,
  ApiBearerAuth,
} from '@nestjs/swagger';
import { EntityService } from './entity.service';
import { EntityRecord } from './entity.entity';
import { CreateEntityDto } from './dto/create-entity.dto';
import { UpdateEntityDto } from './dto/update-entity.dto';
import { UpdatePositionDto } from './dto/update-position.dto';
import { QueryEntitiesDto, NearbyQueryDto } from './dto/query-entities.dto';
import { EntityWithDistance, EntityCountByType } from './entity.repository';

@ApiTags('entities')
@ApiBearerAuth('keycloak-jwt')
@Controller('entities')
export class EntityController {
  private readonly logger = new Logger(EntityController.name);

  constructor(private readonly entityService: EntityService) {}

  // ─── LIST / BOUNDING BOX QUERY ────────────────────────────────────────

  @Get()
  @ApiOperation({
    summary: 'Query entities',
    description:
      'Retrieve entities with optional bounding-box spatial filter, type/source/classification filters, and pagination.',
  })
  @ApiResponse({ status: 200, description: 'Paginated list of entities' })
  async findAll(
    @Query() query: QueryEntitiesDto,
  ): Promise<{ data: EntityRecord[]; total: number; page: number; pageSize: number }> {
    this.logger.debug(`Query entities: ${JSON.stringify(query)}`);
    return this.entityService.findWithinBoundingBox(query);
  }

  // ─── NEARBY QUERY ─────────────────────────────────────────────────────

  @Get('nearby')
  @ApiOperation({
    summary: 'Find nearby entities',
    description:
      'Find entities within a given radius (meters) of a geographic point, ordered by distance.',
  })
  @ApiResponse({ status: 200, description: 'List of entities with distance in meters' })
  async findNearby(
    @Query() query: NearbyQueryDto,
  ): Promise<EntityWithDistance[]> {
    this.logger.debug(`Nearby query: lat=${query.lat}, lng=${query.lng}, radius=${query.radius}`);
    return this.entityService.findNearby(query.lat, query.lng, query.radius ?? 10000);
  }

  // ─── COUNTS / STATS ──────────────────────────────────────────────────

  @Get('counts')
  @ApiOperation({
    summary: 'Get entity counts',
    description: 'Aggregate counts of entities grouped by type and classification.',
  })
  @ApiResponse({ status: 200, description: 'Entity count breakdown' })
  async getCounts(): Promise<EntityCountByType[]> {
    return this.entityService.getEntityCount();
  }

  // ─── GET BY ID ────────────────────────────────────────────────────────

  @Get(':id')
  @ApiOperation({ summary: 'Get entity by ID' })
  @ApiParam({ name: 'id', type: 'string', format: 'uuid' })
  @ApiResponse({ status: 200, description: 'The entity record' })
  @ApiResponse({ status: 404, description: 'Entity not found' })
  async findOne(
    @Param('id', new ParseUUIDPipe({ version: '4' })) id: string,
  ): Promise<EntityRecord> {
    return this.entityService.findById(id);
  }

  // ─── CREATE ───────────────────────────────────────────────────────────

  @Post()
  @HttpCode(HttpStatus.CREATED)
  @ApiOperation({
    summary: 'Create a new entity',
    description: 'Create a new tracked entity with optional initial position.',
  })
  @ApiResponse({ status: 201, description: 'Entity created successfully' })
  @ApiResponse({ status: 400, description: 'Validation error' })
  async create(@Body() dto: CreateEntityDto): Promise<EntityRecord> {
    this.logger.log(`Creating entity: ${dto.name} (${dto.entityType})`);
    return this.entityService.create(dto);
  }

  // ─── UPDATE ───────────────────────────────────────────────────────────

  @Patch(':id')
  @ApiOperation({ summary: 'Update an entity' })
  @ApiParam({ name: 'id', type: 'string', format: 'uuid' })
  @ApiResponse({ status: 200, description: 'Entity updated successfully' })
  @ApiResponse({ status: 404, description: 'Entity not found' })
  async update(
    @Param('id', new ParseUUIDPipe({ version: '4' })) id: string,
    @Body() dto: UpdateEntityDto,
  ): Promise<EntityRecord> {
    this.logger.log(`Updating entity: ${id}`);
    return this.entityService.update(id, dto);
  }

  // ─── UPDATE POSITION ─────────────────────────────────────────────────

  @Patch(':id/position')
  @ApiOperation({
    summary: 'Update entity position',
    description:
      'Update the geospatial position and kinematic state of an entity. Publishes a position event.',
  })
  @ApiParam({ name: 'id', type: 'string', format: 'uuid' })
  @ApiResponse({ status: 200, description: 'Position updated successfully' })
  @ApiResponse({ status: 404, description: 'Entity not found' })
  async updatePosition(
    @Param('id', new ParseUUIDPipe({ version: '4' })) id: string,
    @Body() dto: UpdatePositionDto,
  ): Promise<EntityRecord> {
    this.logger.debug(`Updating position for entity ${id}`);
    return this.entityService.updatePosition(id, dto);
  }

  // ─── DELETE ───────────────────────────────────────────────────────────

  @Delete(':id')
  @HttpCode(HttpStatus.NO_CONTENT)
  @ApiOperation({
    summary: 'Delete an entity',
    description: 'Soft-delete an entity. The record is retained but marked as deleted.',
  })
  @ApiParam({ name: 'id', type: 'string', format: 'uuid' })
  @ApiResponse({ status: 204, description: 'Entity deleted successfully' })
  @ApiResponse({ status: 404, description: 'Entity not found' })
  async delete(
    @Param('id', new ParseUUIDPipe({ version: '4' })) id: string,
  ): Promise<void> {
    this.logger.log(`Deleting entity: ${id}`);
    return this.entityService.delete(id);
  }
}
