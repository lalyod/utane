-- Listening log behind the home page. id is chosen by the client so a checkpoint can be re-sent safely;
-- seconds only grows. updated_at (unix ms) lets device sync tell which side has the longer listen.
CREATE TABLE plays (
    id TEXT PRIMARY KEY,
    track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    played_at INTEGER NOT NULL,
    seconds INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX plays_played_at ON plays(played_at);
CREATE INDEX plays_track ON plays(track_id);
