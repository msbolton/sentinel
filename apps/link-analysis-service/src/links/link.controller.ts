import {
  Controller,
  Get,
  Post,
  Delete,
  Param,
  Query,
  Body,
  ParseUUIDPipe,
  HttpCode,
  HttpStatus,
} from '@nestjs/common';
import { LinkService } from './link.service';
import { CreateLinkDto } from './dto/create-link.dto';
import { QueryLinkDto, QueryGraphDto, ShortestPathDto } from './dto/query-link.dto';

@Controller('links')
export class LinkController {
  constructor(private readonly linkService: LinkService) {}

  /**
   * GET /links?entityId=&types[]=&minConfidence=
   * Query links for a given entity.
   */
  @Get()
  async getLinks(@Query() query: QueryLinkDto) {
    return this.linkService.getLinks(
      query.entityId,
      query.types,
      query.minConfidence,
    );
  }

  /**
   * GET /links/graph?centerId=&maxDepth=&types[]=&minConfidence=
   * Traverse the entity relationship graph from a center entity.
   */
  @Get('graph')
  async getGraph(@Query() query: QueryGraphDto) {
    return this.linkService.getGraph(
      query.centerId,
      query.maxDepth,
      query.types,
      query.minConfidence,
    );
  }

  /**
   * GET /links/shortest-path?from=&to=
   * Find the shortest path between two entities in the graph.
   */
  @Get('shortest-path')
  async getShortestPath(@Query() query: ShortestPathDto) {
    return this.linkService.findShortestPath(query.from, query.to);
  }

  /**
   * GET /links/communities
   * Detect communities in the entity graph.
   */
  @Get('communities')
  async detectCommunities() {
    return this.linkService.detectCommunities();
  }

  /**
   * POST /links
   * Create a new link between two entities.
   */
  @Post()
  @HttpCode(HttpStatus.CREATED)
  async createLink(@Body() dto: CreateLinkDto) {
    return this.linkService.createLink(dto);
  }

  /**
   * DELETE /links/:id
   * Delete a link by ID.
   */
  @Delete(':id')
  @HttpCode(HttpStatus.NO_CONTENT)
  async deleteLink(@Param('id', ParseUUIDPipe) id: string) {
    await this.linkService.deleteLink(id);
  }
}
