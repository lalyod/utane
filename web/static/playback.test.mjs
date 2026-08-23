import test from "node:test";
import assert from "node:assert/strict";

import { clampTime, parsePlaybackState, resumeTime } from "./playback.mjs";

test("validates persisted playback state", () => {
  assert.deepEqual(parsePlaybackState('{"trackID":"track-1","time":42,"volume":0.7,"muted":false}'), {
    trackID: "track-1",
    time: 42,
    volume: 0.7,
    muted: false,
  });
  assert.equal(parsePlaybackState("not json"), null);
  assert.equal(parsePlaybackState('{"trackID":"","time":-1,"volume":2,"muted":"no"}'), null);
});

test("clamps seeking and restores exact saved playback", () => {
  assert.equal(clampTime(-2, 100), 0);
  assert.equal(clampTime(120, 100), 100);
  assert.equal(resumeTime(42, 100), 42);
  assert.equal(resumeTime(99.5, 100), 99.5);
});
