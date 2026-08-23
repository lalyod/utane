import test from "node:test";
import assert from "node:assert/strict";

import { trackArtworkURL } from "./artwork.mjs";

test("track artwork prefers an override, then its album, then the fallback", () => {
  const album = { id: "album-1", has_cover: true };

  assert.equal(trackArtworkURL({ id: "track-1", has_cover: true }, album), "/music/tracks/track-1/cover");
  assert.equal(trackArtworkURL({ id: "track-1", has_cover: false }, album), "/music/albums/album-1/cover");
  assert.equal(trackArtworkURL({ id: "track-1", has_cover: false }), "");
});
