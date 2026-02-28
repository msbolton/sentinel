import {
  Controller,
  Get,
  Post,
  Put,
  Delete,
  Param,
  Query,
  Body,
  UseGuards,
  HttpCode,
  HttpStatus,
  ParseUUIDPipe,
  Logger,
} from '@nestjs/common';
import {
  ApiTags,
  ApiOperation,
  ApiResponse,
  ApiBearerAuth,
  ApiParam,
  ApiQuery,
} from '@nestjs/swagger';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';
import { Roles } from '../auth/decorators/roles.decorator';
import { Classification } from '../auth/decorators/classification.decorator';
import { EntitiesService, PaginatedEntitiesResult } from './entities.service';
import { CreateEntityDto } from './dto/create-entity.dto';
import { UpdateEntityDto } from './dto/update-entity.dto';
import { QueryEntitiesDto } from './dto/query-entities.dto';

/**
 * REST controller for SENTINEL entity management.
 *
 * Provides CRUD operations and geospatial queries for tracked entities
 * (aircraft, vessels, vehicles, persons of interest, facilities, etc.).
 * All endpoints require JWT authentication and respect classification
 * level enforcement.
 */
@ApiTags('entities')
@ApiBearerAuth('keycloak-jwt')
@Controller('entities')
@UseGuards(JwtAuthGuard)
export class EntitiesController {
  private readonly logger = new Logger(EntitiesController.name);

  constructor(private readonly entitiesService: EntitiesService) {}

  /**
   * Query entities within a bounding box with optional type and source filters.
   * Supports pagination and classification-based filtering.
   */
  @Get()
  @ApiOperation({
    summary: 'Query entities by bounding box and filters',
    description:
      'Returns entities matching the specified geospatial bounds, entity types, sources, and classification level. Results are paginated.',
  })
  @ApiResponse({
    status: 200,
    description: 'Paginated list of entities matching the query',
  })
  @ApiResponse({ status: 401, description: 'Unauthorized - invalid or missing JWT' })
  @ApiResponse({ status: 403, description: 'Forbidden - insufficient clearance' })
  async queryEntities(
    @Query() query: QueryEntitiesDto,
  ): Promise<PaginatedEntitiesResult> {
    this.logger.debug(
      `Query entities: bbox=[${query.north},${query.south},${query.east},${query.west}] types=${query.entityTypes}`,
    );

    return this.entitiesService.queryEntities(query);
  }

  /**
   * Get a single entity by its unique identifier.
   */
  @Get(':id')
  @ApiOperation({ summary: 'Get entity by ID' })
  @ApiParam({ name: 'id', description: 'Entity UUID', type: String })
  @ApiResponse({ status: 200, description: 'Entity details' })
  @ApiResponse({ status: 404, description: 'Entity not found' })
  async getEntityById(
    @Param('id', new ParseUUIDPipe({ version: '4' })) id: string,
  ): Promise<Record<string, unknown>> {
    this.logger.debug(`Get entity: ${id}`);
    return this.entitiesService.getEntityById(id);
  }

  /**
   * Create a new tracked entity.
   * Requires the 'analyst' or 'admin' role.
   */
  @Post()
  @Roles('analyst', 'admin')
  @HttpCode(HttpStatus.CREATED)
  @ApiOperation({ summary: 'Create a new entity' })
  @ApiResponse({ status: 201, description: 'Entity created successfully' })
  @ApiResponse({ status: 400, description: 'Invalid request body' })
  @ApiResponse({
    status: 403,
    description: 'Forbidden - insufficient role or clearance',
  })
  async createEntity(
    @Body() dto: CreateEntityDto,
  ): Promise<Record<string, unknown>> {
    this.logger.log(`Create entity: type=${dto.entityType}, name=${dto.name}`);
    return this.entitiesService.createEntity(dto);
  }

  /**
   * Update an existing entity.
   * Requires the 'analyst' or 'admin' role.
   */
  @Put(':id')
  @Roles('analyst', 'admin')
  @ApiOperation({ summary: 'Update an existing entity' })
  @ApiParam({ name: 'id', description: 'Entity UUID', type: String })
  @ApiResponse({ status: 200, description: 'Entity updated successfully' })
  @ApiResponse({ status: 404, description: 'Entity not found' })
  async updateEntity(
    @Param('id', new ParseUUIDPipe({ version: '4' })) id: string,
    @Body() dto: UpdateEntityDto,
  ): Promise<Record<string, unknown>> {
    this.logger.log(`Update entity: ${id}`);
    return this.entitiesService.updateEntity(id, dto);
  }

  /**
   * Delete (soft-delete) an entity.
   * Requires the 'admin' role.
   * Entities classified SECRET or above require TOP_SECRET clearance to delete.
   */
  @Delete(':id')
  @Roles('admin')
  @Classification('SECRET')
  @HttpCode(HttpStatus.NO_CONTENT)
  @ApiOperation({ summary: 'Delete an entity' })
  @ApiParam({ name: 'id', description: 'Entity UUID', type: String })
  @ApiResponse({ status: 204, description: 'Entity deleted' })
  @ApiResponse({ status: 404, description: 'Entity not found' })
  async deleteEntity(
    @Param('id', new ParseUUIDPipe({ version: '4' })) id: string,
  ): Promise<void> {
    this.logger.log(`Delete entity: ${id}`);
    await this.entitiesService.deleteEntity(id);
  }
}
