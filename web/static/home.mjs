export const greeting = (date) => {
  const hour = date.getHours();
  return hour < 5 ? "Good night" : hour < 12 ? "Good morning" : hour < 18 ? "Good afternoon" : "Good evening";
};

// Measures real listening time for one track. timeupdate fires about 4 times a second during
// playback, so only small forward steps count: a seek or a pause never inflates the total.
export class ListenTimer {
  constructor(newID = () => crypto.randomUUID()) {
    this.newID = newID;
    this.reset("");
  }

  reset(trackID) {
    this.trackID = trackID;
    this.id = trackID ? this.newID() : "";
    this.seconds = 0;
    this.sent = 0;
    this.last = null;
  }

  pause() {
    this.last = null;
  }

  tick(time) {
    if (this.last !== null) {
      const delta = time - this.last;
      if (delta > 0 && delta < 2) this.seconds += delta;
    }
    this.last = time;
  }

  // The play to store, or null. A checkpoint goes out every 30 listened seconds and a final one
  // when the track is left; listens under 5 seconds are noise.
  checkpoint(final) {
    const seconds = Math.floor(this.seconds);
    if (!this.trackID || seconds < 5 || seconds <= this.sent) return null;
    if (!final && seconds - this.sent < 30) return null;
    this.sent = seconds;
    return { id: this.id, track_id: this.trackID, seconds };
  }
}
