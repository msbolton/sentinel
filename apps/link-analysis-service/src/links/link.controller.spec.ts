import { Test, TestingModule } from '@nestjs/testing';
import { LinkController } from './link.controller';
import { LinkService } from './link.service';

describe('LinkController', () => {
  let controller: LinkController;
  let linkService: {
    getLinks: jest.Mock;
    getGraph: jest.Mock;
    findShortestPath: jest.Mock;
    detectCommunities: jest.Mock;
    createLink: jest.Mock;
    deleteLink: jest.Mock;
  };

  beforeEach(async () => {
    linkService = {
      getLinks: jest.fn(),
      getGraph: jest.fn(),
      findShortestPath: jest.fn(),
      detectCommunities: jest.fn(),
      createLink: jest.fn(),
      deleteLink: jest.fn(),
    };

    const module: TestingModule = await Test.createTestingModule({
      controllers: [LinkController],
      providers: [
        { provide: LinkService, useValue: linkService },
      ],
    }).compile();

    controller = module.get<LinkController>(LinkController);
  });

  it('should be defined', () => {
    expect(controller).toBeDefined();
  });

  // ─── GET /links ───────────────────────────────────────────────────────

  describe('getLinks', () => {
    it('should call linkService.getLinks with entityId, types, and minConfidence', async () => {
      const query = {
        entityId: 'uuid-entity-1',
        types: ['ASSOCIATED_WITH'],
        minConfidence: 0.7,
      };
      const mockLinks = [
        {
          id: 'uuid-link-1',
          sourceEntityId: 'uuid-entity-1',
          targetEntityId: 'uuid-entity-2',
          linkType: 'ASSOCIATED_WITH',
          confidence: 0.9,
        },
      ];
      linkService.getLinks.mockResolvedValue(mockLinks);

      const result = await controller.getLinks(query as any);

      expect(linkService.getLinks).toHaveBeenCalledWith(
        'uuid-entity-1',
        ['ASSOCIATED_WITH'],
        0.7,
      );
      expect(result).toEqual(mockLinks);
    });

    it('should pass undefined for optional fields when not provided', async () => {
      const query = { entityId: 'uuid-entity-1' };
      linkService.getLinks.mockResolvedValue([]);

      await controller.getLinks(query as any);

      expect(linkService.getLinks).toHaveBeenCalledWith(
        'uuid-entity-1',
        undefined,
        undefined,
      );
    });
  });

  // ─── GET /links/graph ─────────────────────────────────────────────────

  describe('getGraph', () => {
    it('should call linkService.getGraph with centerId, maxDepth, types, and minConfidence', async () => {
      const query = {
        centerId: 'uuid-center',
        maxDepth: 2,
        types: ['ASSOCIATED_WITH', 'LOCATED_AT'],
        minConfidence: 0.5,
      };
      const mockGraph = {
        nodes: [{ id: '1', entityId: 'uuid-center', label: 'Center', properties: {} }],
        edges: [],
      };
      linkService.getGraph.mockResolvedValue(mockGraph);

      const result = await controller.getGraph(query as any);

      expect(linkService.getGraph).toHaveBeenCalledWith(
        'uuid-center',
        2,
        ['ASSOCIATED_WITH', 'LOCATED_AT'],
        0.5,
      );
      expect(result).toEqual(mockGraph);
    });
  });

  // ─── GET /links/shortest-path ─────────────────────────────────────────

  describe('getShortestPath', () => {
    it('should call linkService.findShortestPath with from and to entity IDs', async () => {
      const query = {
        from: 'uuid-from',
        to: 'uuid-to',
      };
      const mockPath = {
        nodes: [
          { id: '1', entityId: 'uuid-from', label: 'A', properties: {} },
          { id: '2', entityId: 'uuid-to', label: 'B', properties: {} },
        ],
        edges: [
          {
            id: 'e1',
            sourceEntityId: 'uuid-from',
            targetEntityId: 'uuid-to',
            linkType: 'ASSOCIATED_WITH',
            confidence: 0.9,
            properties: {},
          },
        ],
      };
      linkService.findShortestPath.mockResolvedValue(mockPath);

      const result = await controller.getShortestPath(query as any);

      expect(linkService.findShortestPath).toHaveBeenCalledWith('uuid-from', 'uuid-to');
      expect(result).toEqual(mockPath);
    });
  });

  // ─── GET /links/communities ───────────────────────────────────────────

  describe('detectCommunities', () => {
    it('should call linkService.detectCommunities', async () => {
      const mockCommunities = [
        { communityId: 0, members: ['uuid-1', 'uuid-2'] },
        { communityId: 1, members: ['uuid-3', 'uuid-4'] },
      ];
      linkService.detectCommunities.mockResolvedValue(mockCommunities);

      const result = await controller.detectCommunities();

      expect(linkService.detectCommunities).toHaveBeenCalled();
      expect(result).toEqual(mockCommunities);
    });
  });

  // ─── POST /links ──────────────────────────────────────────────────────

  describe('createLink', () => {
    it('should call linkService.createLink with the dto', async () => {
      const dto = {
        sourceEntityId: 'uuid-source',
        targetEntityId: 'uuid-target',
        linkType: 'ASSOCIATED_WITH',
        confidence: 0.85,
        description: 'Known associate',
      };
      const mockCreated = { id: 'uuid-link-new', ...dto };
      linkService.createLink.mockResolvedValue(mockCreated);

      const result = await controller.createLink(dto as any);

      expect(linkService.createLink).toHaveBeenCalledWith(dto);
      expect(result).toEqual(mockCreated);
    });
  });

  // ─── DELETE /links/:id ────────────────────────────────────────────────

  describe('deleteLink', () => {
    it('should call linkService.deleteLink with the id', async () => {
      linkService.deleteLink.mockResolvedValue(undefined);

      await controller.deleteLink('uuid-link-1');

      expect(linkService.deleteLink).toHaveBeenCalledWith('uuid-link-1');
    });
  });
});
