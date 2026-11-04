package core

const schema = `
CREATE TABLE IF NOT EXISTS players (
	id         INTEGER PRIMARY KEY,
	name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
	slack_id   TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS matches (
	id         INTEGER PRIMARY KEY,
	played_at  TEXT NOT NULL,
	mode       TEXT NOT NULL,            -- 1v1 | ffa | team
	source     TEXT NOT NULL DEFAULT '', -- vision | manual | slack | web
	note       TEXT NOT NULL DEFAULT '',
	deleted    INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_matches_played ON matches(played_at, id);

CREATE TABLE IF NOT EXISTS entries (
	id           INTEGER PRIMARY KEY,
	match_id     INTEGER NOT NULL REFERENCES matches(id),
	player_id    INTEGER NOT NULL REFERENCES players(id),
	character    TEXT NOT NULL DEFAULT '',
	team         INTEGER NOT NULL,
	rank         INTEGER NOT NULL,
	port         INTEGER NOT NULL DEFAULT 0,
	stocks       INTEGER
);
CREATE INDEX IF NOT EXISTS idx_entries_match ON entries(match_id);
CREATE INDEX IF NOT EXISTS idx_entries_player ON entries(player_id);

-- current rating per (scope, player). scope: overall | mode:<mode> | char:<key>
CREATE TABLE IF NOT EXISTS rating_states (
	scope     TEXT NOT NULL,
	player_id INTEGER NOT NULL REFERENCES players(id),
	mu        REAL NOT NULL,
	sigma     REAL NOT NULL,
	games     INTEGER NOT NULL DEFAULT 0,
	wins      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (scope, player_id)
);

CREATE TABLE IF NOT EXISTS rating_history (
	id           INTEGER PRIMARY KEY,
	match_id     INTEGER NOT NULL REFERENCES matches(id),
	player_id    INTEGER NOT NULL REFERENCES players(id),
	scope        TEXT NOT NULL,
	mu_before    REAL NOT NULL,
	sigma_before REAL NOT NULL,
	mu_after     REAL NOT NULL,
	sigma_after  REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_history_player ON rating_history(player_id, scope, match_id);

-- perceptual-hash templates learned for the results screen
CREATE TABLE IF NOT EXISTS vision_templates (
	id         INTEGER PRIMARY KEY,
	kind       TEXT NOT NULL,   -- screen | char | rank
	label      TEXT NOT NULL,
	hash       INTEGER NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_templates_kind ON vision_templates(kind);
`
