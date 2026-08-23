import test from "node:test";
import assert from "node:assert/strict";

import { activeAt, centeredScrollTop, parseEnhancedLRC, scriptLang } from "./lyrics.mjs";

test("parses and orders enhanced LRC without inventing word timings", () => {
  const lines = parseEnhancedLRC([
    "[00:02.00]<00:02.00>Hello <00:02.50>world",
    "[ar:Artist]",
    "[00:01.00]Line timed only",
  ].join("\n"));

  assert.deepEqual(lines, [
    { time: 1, text: "Line timed only", words: [] },
    {
      time: 2,
      text: "Hello world",
      words: [
        { time: 2, text: "Hello " },
        { time: 2.5, text: "world" },
      ],
    },
  ]);
});

test("finds the latest timestamp at or before playback", () => {
  const items = [{ time: 1 }, { time: 2.5 }, { time: 4 }];

  assert.equal(activeAt(items, 0.5), -1);
  assert.equal(activeAt(items, 2.5), 1);
  assert.equal(activeAt(items, 10), 2);
});

test("detects Hangul and kana for lyric presentation", () => {
  assert.equal(scriptLang("English only"), "");
  assert.equal(scriptLang("사랑해"), "ko");
  assert.equal(scriptLang("アイウエオ"), "ja");
  assert.equal(scriptLang("ひらがな"), "ja");
  assert.equal(scriptLang("한글 and カナ"), "ko");
});

test("preserves untimed native text before enhanced word timing", () => {
  assert.deepEqual(parseEnhancedLRC("[00:01.00]君の<00:02.00>声"), [{
    time: 1,
    text: "君の声",
    prefix: "君の",
    words: [{ time: 2, text: "声" }],
  }]);
});

test("centers an active line using only panel-relative scrolling", () => {
  assert.equal(centeredScrollTop(200, 100, 600, 500, 40), 320);
  assert.equal(centeredScrollTop(0, 100, 600, 120, 40), 0);
});
