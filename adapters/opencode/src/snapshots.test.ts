// audit: behavior - one cached memory block per host session, rendered once and replaced only by an explicit refresh.
// audit: regression - a per-connection snapshot bled across sessions and refreshed on unrelated writes.
// audit: coverage - Interleaved sessions, overlapping first renders, failed renders, and refresh of one session.
// audit: seam - A recording engine client and in-memory session cache stand in for the host context hook.

import { describe, expect, test } from "bun:test";
import { MEMORY_MARKER } from "./events";
import { createSnapshotCache } from "./snapshots";

function fakeClient() {
  const calls: any[] = [];
  let snapshot = { snapshotID: "snap-1", section: "MEMORY SECTION" };
  return {
    calls,
    contextGet: async (params: any) => {
      calls.push(params);
      return snapshot;
    },
    setSnapshot(next: { snapshotID: string; section: string }) {
      snapshot = next;
    },
  };
}

describe("session snapshot cache", () => {
  test("renders once per session and reuses the cached block", async () => {
    const client = fakeClient();
    const cache = createSnapshotCache(client as any);

    const first = await cache.ensure("s1");
    const second = await cache.ensure("s1");

    expect(second).toBe(first);
    expect(client.calls).toEqual([{ refresh: true }]);
    expect(first).toContain(MEMORY_MARKER);
    expect(first).toContain("snap-1");
  });

  test("keeps two sessions independent", async () => {
    const client = fakeClient();
    const cache = createSnapshotCache(client as any);

    const first = await cache.ensure("s1");
    client.setSnapshot({ snapshotID: "snap-2", section: "FRESH SECTION" });
    const other = await cache.ensure("s2");
    const again = await cache.ensure("s1");

    expect(first).not.toBe(other);
    expect(again).toBe(first);
    expect(other).toContain("snap-2");
    expect(client.calls).toEqual([{ refresh: true }, { refresh: true }]);
  });

  test("shares one render for overlapping first requests", async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const calls: any[] = [];
    const client = {
      contextGet: async (params: any) => {
        calls.push(params);
        await gate;
        return { snapshotID: "snap-shared", section: "SHARED" };
      },
    };
    const cache = createSnapshotCache(client);

    const both = Promise.all([cache.ensure("s1"), cache.ensure("s1")]);
    release();
    const [a, b] = await both;

    expect(a).toBe(b);
    expect(calls).toEqual([{ refresh: true }]);
  });

  test("refresh replaces only the calling session", async () => {
    const client = fakeClient();
    const cache = createSnapshotCache(client as any);
    const original = await cache.ensure("s1");
    await cache.ensure("s2");
    client.setSnapshot({ snapshotID: "snap-3", section: "REFRESHED" });

    const refreshed = await cache.refresh("s1");

    expect(refreshed).not.toBe(original);
    expect(refreshed).toContain("snap-3");
    expect(await cache.ensure("s1")).toBe(refreshed);
    expect(await cache.ensure("s2")).toContain("snap-1");
    expect(client.calls).toEqual([{ refresh: true }, { refresh: true }, { refresh: true }]);
  });

  test("keeps the last safe snapshot when a refresh fails", async () => {
    let calls = 0;
    const client = {
      contextGet: async () => {
        calls++;
        if (calls === 1) return { snapshotID: "snap-1", section: "MEMORY SECTION" };
        throw new Error("engine down");
      },
    };
    const cache = createSnapshotCache(client);
    const original = await cache.ensure("s1");

    expect(await cache.refresh("s1")).toBeNull();
    expect(cache.block("s1")).toBe(original);
  });
});
