import {
  Controller,
  Get,
  Post,
  Patch,
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
} from '@nestjs/swagger';
import { JwtAuthGuard } from '../auth/jwt-auth.guard';
import { Roles } from '../auth/decorators/roles.decorator';
import { LocationsService } from './locations.service';
import { LocationRecord } from './location.entity';
import { CreateLocationDto } from './dto/create-location.dto';
import { UpdateLocationDto } from './dto/update-location.dto';
import { QueryLocationsDto } from './dto/query-locations.dto';

@ApiTags('locations')
@ApiBearerAuth('keycloak-jwt')
@Controller('locations')
@UseGuards(JwtAuthGuard)
export class LocationsController {
  private readonly logger = new Logger(LocationsController.name);

  constructor(private readonly locationsService: LocationsService) {}

  @Get()
  @ApiOperation({ summary: 'List all locations with optional filters' })
  @ApiResponse({ status: 200, description: 'List of locations' })
  async findAll(@Query() query: QueryLocationsDto): Promise<LocationRecord[]> {
    return this.locationsService.findAll(query);
  }

  @Get(':id')
  @ApiOperation({ summary: 'Get a location by ID' })
  @ApiParam({ name: 'id', description: 'Location UUID', type: String })
  @ApiResponse({ status: 200, description: 'Location details' })
  @ApiResponse({ status: 404, description: 'Location not found' })
  async findOne(
    @Param('id', new ParseUUIDPipe({ version: '4' })) id: string,
  ): Promise<LocationRecord> {
    return this.locationsService.findOne(id);
  }

  @Post()
  @Roles('analyst', 'admin')
  @HttpCode(HttpStatus.CREATED)
  @ApiOperation({ summary: 'Create a new location' })
  @ApiResponse({ status: 201, description: 'Location created' })
  @ApiResponse({ status: 400, description: 'Invalid request body' })
  async create(@Body() dto: CreateLocationDto): Promise<LocationRecord> {
    this.logger.log(`Create location: ${dto.name}`);
    return this.locationsService.create(dto);
  }

  @Patch(':id')
  @Roles('analyst', 'admin')
  @ApiOperation({ summary: 'Update an existing location' })
  @ApiParam({ name: 'id', description: 'Location UUID', type: String })
  @ApiResponse({ status: 200, description: 'Location updated' })
  @ApiResponse({ status: 404, description: 'Location not found' })
  async update(
    @Param('id', new ParseUUIDPipe({ version: '4' })) id: string,
    @Body() dto: UpdateLocationDto,
  ): Promise<LocationRecord> {
    this.logger.log(`Update location: ${id}`);
    return this.locationsService.update(id, dto);
  }

  @Delete(':id')
  @Roles('admin')
  @HttpCode(HttpStatus.NO_CONTENT)
  @ApiOperation({ summary: 'Delete a location' })
  @ApiParam({ name: 'id', description: 'Location UUID', type: String })
  @ApiResponse({ status: 204, description: 'Location deleted' })
  @ApiResponse({ status: 404, description: 'Location not found' })
  async remove(
    @Param('id', new ParseUUIDPipe({ version: '4' })) id: string,
  ): Promise<void> {
    this.logger.log(`Delete location: ${id}`);
    await this.locationsService.remove(id);
  }
}
