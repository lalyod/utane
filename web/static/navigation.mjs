export const viewForPath = (path) => path === "/manage" ? "manage" : path === "/playlists" ? "playlists" : "player";

export const adjacentTrackID = (tracks, currentID, offset) => {
  const index = tracks.findIndex((track) => track.id === currentID);
  if (index === -1) return "";
  const next = index + offset;
  return next >= 0 && next < tracks.length ? tracks[next].id : "";
};
