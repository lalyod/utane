import test from "node:test";
import assert from "node:assert/strict";

import { ListenTimer, greeting } from "./home.mjs";

const timer = () => {
  let n = 0;
  return new ListenTimer(() => `play-${++n}`);
};

const play = (t, from, to, step = 0.25) => {
  for (let time = from; time <= to + 1e-9; time += step) t.tick(time);
};

test("greeting follows the hour", () => {
  const at = (hour) => greeting(new Date(2026, 0, 1, hour));
  assert.equal(at(3), "Good night");
  assert.equal(at(9), "Good morning");
  assert.equal(at(14), "Good afternoon");
  assert.equal(at(21), "Good evening");
});

test("only continuous playback counts, a seek does not", () => {
  const t = timer();
  t.reset("a");
  play(t, 0, 10);
  t.tick(150); // seek forward
  play(t, 150, 160);
  assert.equal(Math.floor(t.seconds), 20);
  t.tick(30); // seek back
  assert.equal(Math.floor(t.seconds), 20);
});

test("a pause does not count and resuming restarts the clock", () => {
  const t = timer();
  t.reset("a");
  play(t, 0, 10);
  t.pause();
  play(t, 10.25, 20);
  assert.ok(t.seconds >= 19.5 && t.seconds <= 20, t.seconds);
});

test("checkpoints every 30 seconds and a final one, ignoring listens under 5s", () => {
  const t = timer();
  t.reset("a");
  play(t, 0, 4);
  assert.equal(t.checkpoint(true), null);

  play(t, 4.25, 29);
  assert.equal(t.checkpoint(false), null);
  play(t, 29.25, 31);
  assert.deepEqual(t.checkpoint(false), { id: "play-1", track_id: "a", seconds: 31 });
  assert.equal(t.checkpoint(false), null);

  play(t, 31.25, 40);
  assert.equal(t.checkpoint(false), null);
  assert.deepEqual(t.checkpoint(true), { id: "play-1", track_id: "a", seconds: 40 });
  assert.equal(t.checkpoint(true), null);
});

test("a new track starts a new play", () => {
  const t = timer();
  t.reset("a");
  play(t, 0, 10);
  const first = t.checkpoint(true);
  t.reset("a");
  play(t, 0, 10);
  const second = t.checkpoint(true);
  assert.notEqual(first.id, second.id);
});
