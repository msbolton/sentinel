import { Classification, EntityType } from '../../shared/models/entity.model';
import { LinkType } from '../../shared/models/link.model';
import { edgeColor, linkTypeLabel, nodeColor, toVisGraph } from './graph-view';

describe('toVisGraph', () => {
  const graph = {
    nodes: [
      { entityId: 'c', entityType: EntityType.VESSEL, name: 'Centre', classification: Classification.UNCLASSIFIED },
      { entityId: 'n', entityType: EntityType.PERSON, name: 'Neighbour', classification: Classification.SECRET },
    ],
    edges: [{ id: 'e1', sourceEntityId: 'c', targetEntityId: 'n', linkType: LinkType.COMMAND_CONTROL, confidence: 0.75 }],
  };

  it('maps entities to vis nodes and links to vis edges', () => {
    const { nodes, edges } = toVisGraph(graph, 'c', 'Mono');
    expect(nodes.map((n) => [n.id, n.label])).toEqual([
      ['c', 'Centre'],
      ['n', 'Neighbour'],
    ]);
    expect(nodes[0].size).toBeGreaterThan(nodes[1].size);
    expect(nodes[1].color.background).toBe(nodeColor(EntityType.PERSON));
    expect(edges).toHaveLength(1);
    expect(edges[0]).toMatchObject({ id: 'e1', from: 'c', to: 'n', label: 'command control', title: 'command control · 75%' });
    expect(edges[0].width).toBeCloseTo(3.25);
    expect(edges[0].color.color).toBe(edgeColor(LinkType.COMMAND_CONTROL));
    expect(edges[0].font.face).toBe('Mono');
  });

  it('has a colour for every link type', () => {
    const fallback = edgeColor('NOT_A_TYPE');
    for (const t of Object.values(LinkType)) {
      if (t === LinkType.UNSPECIFIED) continue;
      expect(edgeColor(t)).not.toBe(fallback);
    }
    expect(linkTypeLabel(LinkType.MOVEMENT_PATTERN)).toBe('movement pattern');
  });
});
