const timestamp = (minutes, seconds) => Number(minutes) * 60 + Number(seconds);

export const parseEnhancedLRC = (input) => input.split(/\r?\n/).flatMap((source) => {
  const lineMatch = source.match(/^\[(\d+):(\d{2}(?:\.\d{1,3})?)\]/);
  if (!lineMatch) return [];

  const body = source.slice(lineMatch[0].length);
  const matches = [...body.matchAll(/<(\d+):(\d{2}(?:\.\d{1,3})?)>([^<]*)/g)];
  const words = matches.map((match) => ({
    time: timestamp(match[1], match[2]),
    text: match[3],
  }));

  return [{
    time: timestamp(lineMatch[1], lineMatch[2]),
    text: body.replace(/<\d+:\d{2}(?:\.\d{1,3})?>/g, ""),
    words,
    ...(matches[0]?.index ? { prefix: body.slice(0, matches[0].index) } : {}),
  }];
}).sort((a, b) => a.time - b.time);

export const activeAt = (items, time) => {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    if (items[index].time <= time) return index;
  }
  return -1;
};

export const centeredScrollTop = (scrollTop, containerTop, containerHeight, targetTop, targetHeight) => Math.max(
  0,
  scrollTop + targetTop - containerTop - (containerHeight - targetHeight) / 2,
);

export const scriptLang = (text) => {
  if (/\p{Script=Hangul}/u.test(text)) return "ko";
  if (/[\p{Script=Hiragana}\p{Script=Katakana}]/u.test(text)) return "ja";
  return "";
};
