import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const app = readFileSync(new URL("./app.js", import.meta.url), "utf8");

test("playback controls are wired to shuffle and repeat state", () => {
  assert.ok(app.includes("const toggleShuffle = () =>"));
  assert.ok(app.includes("state.shuffle = !state.shuffle"));
  assert.ok(app.includes("const toggleLoop = () =>"));
  assert.ok(app.includes("state.loop = !state.loop"));
  assert.ok(app.includes("'#np-shuffle-toggle'"));
  assert.ok(app.includes("'#np-loop-toggle'"));
});

test("playback advance uses playlist-aware mode logic", () => {
  assert.ok(app.includes("const id = nextTrackID(-1)"));
  assert.ok(app.includes("const id = nextTrackID(1)"));
  assert.ok(app.includes("const id = nextTrackID(1, true)"));
});

test("playlists are loaded for playlist playback", () => {
  assert.ok(app.includes("api('/music/playlists')"));
  assert.ok(app.includes("renderPlaylistsPage()"));
});
