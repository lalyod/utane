import test from "node:test";
import assert from "node:assert/strict";

import { adjacentTrackID, viewForPath } from "./navigation.mjs";

test("maps only the manage path to the manage view", () => {
  assert.equal(viewForPath("/"), "player");
  assert.equal(viewForPath("/manage"), "manage");
  assert.equal(viewForPath("/playlists"), "playlists");
  assert.equal(viewForPath("/anything-else"), "player");
});

test("derives adjacent tracks from the current ID after reordering", () => {
  const tracks = [{ id: "c" }, { id: "a" }, { id: "b" }];
  assert.equal(adjacentTrackID(tracks, "a", -1), "c");
  assert.equal(adjacentTrackID(tracks, "a", 1), "b");
  assert.equal(adjacentTrackID(tracks, "c", -1), "");
  assert.equal(adjacentTrackID(tracks, "missing", 1), "");
});

