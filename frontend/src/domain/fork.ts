import type { Message } from './types';

export type ForkSource = { id: string; number: number; title: string };

export function treeNodes(tree: Record<string, unknown> | null): Record<string, unknown>[] {
  return Array.isArray(tree?.nodes) ? tree.nodes : [];
}

export function pathSource(nodes: Record<string, unknown>[], id: string): ForkSource {
  const node = nodes.find((entry) => entry.session_id === id);
  return { id, number: Number(node?.session_number) || 0, title: String(node?.title || '') };
}

export function forkEdge(tree: Record<string, unknown> | null, sessionId?: string) {
  const nodes = treeNodes(tree);
  const node = nodes.find((entry) => entry.session_id === sessionId);
  if (!node?.parent_session_id) return null;
  return {
    source: pathSource(nodes, String(node.parent_session_id)),
    anchor: Number(node.fork_after_message_id) || 0,
    copiedAnchor: Number(node.copied_anchor_message_id) || 0,
  };
}

export function forkPlacement(messages: Message[], edge: NonNullable<ReturnType<typeof forkEdge>>) {
  const note = messages.find(
    (message) =>
      message.role === 'path-note' &&
      message.provenance?.source_session_id === edge.source.id &&
      (message.provenance.anchor_message_id ?? 0) === edge.anchor,
  );
  if (note) return { before: note, count: note.provenance?.source_messages };
  if (!edge.anchor && !edge.copiedAnchor) return { top: true };
  const after = edge.copiedAnchor
    ? messages.reduce<Message | undefined>(
        (last, message) =>
          message.durableRowId === edge.copiedAnchor ||
          (message.durableSourceRowIds as (string | number)[] | undefined)?.some(
            (id) => Number(id) === edge.copiedAnchor,
          )
            ? message
            : last,
        undefined,
      )
    : undefined;
  return { after };
}
