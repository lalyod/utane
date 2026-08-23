export const trackArtworkURL = (track, album) => {
  if (track?.has_cover) return `/music/tracks/${track.id}/cover`;
  if (album?.has_cover) return `/music/albums/${album.id}/cover`;
  return "";
};
