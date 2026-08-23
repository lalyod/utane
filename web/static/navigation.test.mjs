import test from "node:test";
import assert from "node:assert/strict";

import { adjacentTrackID, lyricsLayout, viewForPath } from "./navigation.mjs";

test("maps only the manage path to the manage view", () => {
  assert.equal(viewForPath("/"), "player");
  assert.equal(viewForPath("/manage"), "manage");
  assert.equal(viewForPath("/anything-else"), "player");
});

test("derives adjacent tracks from the current ID after reordering", () => {
  const tracks = [{ id: "c" }, { id: "a" }, { id: "b" }];
  assert.equal(adjacentTrackID(tracks, "a", -1), "c");
  assert.equal(adjacentTrackID(tracks, "a", 1), "b");
  assert.equal(adjacentTrackID(tracks, "c", -1), "");
  assert.equal(adjacentTrackID(tracks, "missing", 1), "");
});

test("lyrics replace the mobile library but sit beside the desktop library", () => {
  assert.deepEqual(lyricsLayout("player", false, true), { library: true, nowPlaying: false, panel: false });
  assert.deepEqual(lyricsLayout("player", true, true), { library: false, nowPlaying: false, panel: true });
  assert.deepEqual(lyricsLayout("player", true, false), { library: false, nowPlaying: true, panel: true });
  assert.deepEqual(lyricsLayout("manage", true, false), { library: false, nowPlaying: false, panel: false });
});
