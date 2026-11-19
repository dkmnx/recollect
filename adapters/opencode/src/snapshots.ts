import { MEMORY_MARKER } from "./events";

export interface Snapshot {
  snapshotID: string;
  section: string;
}

export interface SnapshotCache {
  block(sessionID: string): string | null;
  ensure(sessionID: string): Promise<string | null>;
  refresh(sessionID: string): Promise<string | null>;
  forget(sessionID?: string): void;
}

export function createSnapshotCache(client: {
  contextGet(params: unknown): Promise<Snapshot>;
}): SnapshotCache {
  const blocks = new Map<string, string>();
  const pending = new Map<string, Promise<string | null>>();

  const render = async (sessionID: string): Promise<string | null> => {
    try {
      const snapshot = await client.contextGet({ refresh: true });
      if (!snapshot?.snapshotID || !snapshot?.section) return null;
      const block = `${MEMORY_MARKER}${snapshot.snapshotID} -->\n${snapshot.section}`;
      blocks.set(sessionID, block);
      return block;
    } catch {
      return null;
    }
  };

  return {
    block: (sessionID) => blocks.get(sessionID) ?? null,
    ensure: (sessionID) => {
      const existing = blocks.get(sessionID);
      if (existing) return Promise.resolve(existing);
      const inFlight = pending.get(sessionID);
      if (inFlight) return inFlight;
      const next = render(sessionID).finally(() => pending.delete(sessionID));
      pending.set(sessionID, next);
      return next;
    },
    refresh: (sessionID) => render(sessionID),
    forget: (sessionID) => {
      if (sessionID) blocks.delete(sessionID);
      else blocks.clear();
    },
  };
}
