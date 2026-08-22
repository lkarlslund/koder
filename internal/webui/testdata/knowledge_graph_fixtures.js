'use strict';

const ids = Object.freeze({
  chunk: '019f132e-4f3a-739a-9ab2-5198dcd19e67',
  partition: '01a01f76-1ff6-7c1d-967a-66ad5703dd33',
  format: '01a01f76-1ff6-7c1d-967a-66ad5703dd34',
  verify: '01a01f76-1ff6-7c1d-967a-66ad5703dd35',
  requires: '01a020a6-84d5-7b03-a995-bb2cfb4528b0',
  follows: '01a020a6-84d5-7b03-a995-bb2cfb4528b1',
});

function ref(kind, id) {
  return {kind, id};
}

function revision(number, suffix) {
  return {
    number,
    id: `01a0242c-5a80-73d3-b3ea-05d045d3c${suffix}`,
    actor: {kind: 'system', id: 'fixture'},
    created_at: `2026-08-22T12:00:0${number}Z`,
  };
}

const apiNodes = Object.freeze({
  chunk: {
    id: `chunk:${ids.chunk}`, object: ref('chunk', ids.chunk), semantic_kind: 'project',
    title: 'Disk maintenance', summary: 'Safe disk partitioning procedures.',
    scope: {kind: 'project', selector: 'koder'}, state: 'active', revision: revision(1, 'a01'), risk: ['destructive'],
  },
  partition: {
    id: `entry:${ids.partition}`, object: ref('entry', ids.partition), semantic_kind: 'procedure',
    title: 'Partition a disk with sfdisk', summary: 'Inspect first, then apply an explicit layout.',
    scope: {kind: 'global'}, state: 'active', revision: revision(1, 'a02'), verification: 'verified', risk: ['destructive'],
  },
  format: {
    id: `entry:${ids.format}`, object: ref('entry', ids.format), semantic_kind: 'procedure',
    title: 'Format the new partition', summary: 'Create the selected filesystem only after confirmation.',
    scope: {kind: 'global'}, state: 'draft', revision: revision(1, 'a03'), verification: 'unverified', risk: ['destructive'],
  },
  verify: {
    id: `entry:${ids.verify}`, object: ref('entry', ids.verify), semantic_kind: 'procedure',
    title: 'Verify the partition table', summary: 'Read the resulting table and report it.',
    scope: {kind: 'global'}, state: 'active', revision: revision(1, 'a04'), verification: 'verified', risk: [],
  },
});

const apiEdges = Object.freeze({
  requires: {
    id: ids.requires, source: ref('entry', ids.partition), target: ref('entry', ids.format),
    kind: 'requires', label: 'must happen before', state: 'active', revision: revision(1, 'a05'),
  },
  follows: {
    id: ids.follows, source: ref('entry', ids.format), target: ref('entry', ids.verify),
    kind: 'related_to', label: 'then verify', state: 'active', revision: revision(1, 'a06'),
  },
});

function browserNode(node) {
  return {
    key: node.id,
    attributes: {
      objectKind: node.object.kind, objectID: node.object.id, semanticKind: node.semantic_kind,
      title: node.title, summary: node.summary || '', scopeKind: node.scope.kind,
      scopeSelector: node.scope.selector || '', state: node.state, verification: node.verification || '',
      risk: [...(node.risk || [])], revision: node.revision.number, revisionID: node.revision.id,
    },
  };
}

function graphKey(value) {
  return `${value.kind}:${value.id}`;
}

function browserEdge(edge) {
  return {
    key: edge.id, source: graphKey(edge.source), target: graphKey(edge.target),
    attributes: {
      relationshipKind: edge.kind, label: edge.label || '', state: edge.state,
      revision: edge.revision.number, revisionID: edge.revision.id, directed: true,
    },
  };
}

function patch(checkpoint, values) {
  return {
    patchVersion: 'knowledge.graph.patch.v1', generation: 7,
    checkpoint: {streamID: 'knowledge-fixture-stream', sequence: checkpoint},
    upsertNodes: values.upsertNodes || [], removeNodeKeys: values.removeNodeKeys || [],
    upsertEdges: values.upsertEdges || [], removeEdgeKeys: values.removeEdgeKeys || [],
  };
}

const apiSnapshot = {
  api_version: 'knowledge.v1', request_id: 'knowledge-fixture-request', generation: 7,
  checkpoint: {stream_id: 'knowledge-fixture-stream', sequence: 40},
  nodes: [apiNodes.chunk, apiNodes.partition, apiNodes.format], edges: [apiEdges.requires],
  page: {limit: 100, returned: 3, truncated: false},
};

const snapshotPatch = patch(40, {
  upsertNodes: apiSnapshot.nodes.map(browserNode), upsertEdges: apiSnapshot.edges.map(browserEdge),
});

const formatUpdate = {
  ...apiNodes.format, state: 'active', revision: revision(2, 'a07'), verification: 'partially_verified',
};

const incrementalPatches = [
  patch(41, {upsertNodes: [browserNode(apiNodes.verify)], upsertEdges: [browserEdge(apiEdges.follows)]}),
  patch(42, {upsertNodes: [browserNode(formatUpdate)]}),
  patch(43, {removeEdgeKeys: [ids.requires, ids.follows], removeNodeKeys: [`entry:${ids.format}`]}),
];

function deepFreeze(value) {
  if (!value || typeof value !== 'object' || Object.isFrozen(value)) return value;
  for (const child of Object.values(value)) deepFreeze(child);
  return Object.freeze(value);
}

module.exports = deepFreeze({ids, apiNodes, apiEdges, apiSnapshot, snapshotPatch, incrementalPatches});
