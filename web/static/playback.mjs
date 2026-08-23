export const parsePlaybackState = (raw) => {
  try {
    const value = JSON.parse(raw);
    if (typeof value?.trackID !== "string" || !value.trackID) return null;
    if (!Number.isFinite(value.time) || value.time < 0) return null;
    if (!Number.isFinite(value.volume) || value.volume < 0 || value.volume > 1) return null;
    if (typeof value.muted !== "boolean") return null;
    return value;
  } catch {
    return null;
  }
};

export const clampTime = (time, duration) => Math.min(Math.max(0, time), Number.isFinite(duration) ? duration : time);

export const resumeTime = (time, duration) => clampTime(time, duration);
