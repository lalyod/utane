export const viewForPath = (path) => path === "/manage" ? "manage" : "player";

export const lyricsLayout = (view, open, mobile) => ({
  library: view === "player" && !open,
  nowPlaying: view === "player" && open && !mobile,
  panel: view === "player" && open,
});

export const adjacentTrackID = (tracks, currentID, offset) => {
  const index = tracks.findIndex((track) => track.id === currentID);
  if (index === -1) return "";
  const next = index + offset;
  return next >= 0 && next < tracks.length ? tracks[next].id : "";
};
