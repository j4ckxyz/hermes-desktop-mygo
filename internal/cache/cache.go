// Package cache keeps a local copy of each instance's agents, sessions and
// transcripts in SQLite, so the app opens instantly with the last known
// state and syncs in the background. It is a cache: deleting the file
// loses nothing the instance does not have. Every method does disk I/O;
// callers run them off the UI thread.
package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"hermes-desktop/internal/hermes"

	_ "modernc.org/sqlite"
)

// schemaVersion bumps when the tables change; an older file is rebuilt.
const schemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS profiles (
	instance TEXT NOT NULL, name TEXT NOT NULL, ord INTEGER NOT NULL, data BLOB NOT NULL,
	PRIMARY KEY (instance, name)
);
CREATE TABLE IF NOT EXISTS sessions (
	instance TEXT NOT NULL, id TEXT NOT NULL, profile TEXT NOT NULL,
	last_active REAL NOT NULL, data BLOB NOT NULL,
	PRIMARY KEY (instance, id)
);
CREATE INDEX IF NOT EXISTS sessions_recent ON sessions (instance, last_active DESC);
CREATE TABLE IF NOT EXISTS transcripts (
	instance TEXT NOT NULL, session TEXT NOT NULL, fetched_at REAL NOT NULL, data BLOB NOT NULL,
	PRIMARY KEY (instance, session)
);
`

// Cache is an open cache file. It is safe for concurrent use.
type Cache struct {
	db *sql.DB
}

// DefaultPath is cache.db in the OS cache directory.
func DefaultPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Hermes Desktop", "cache.db"), nil
}

// Open opens (or creates) the cache at path. A file from an older schema,
// or one SQLite cannot read, is replaced: it is only a cache.
func Open(path string) (*Cache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	c, err := open(path)
	if err == nil {
		return c, nil
	}
	// Corrupt or incompatible: start over.
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		os.Remove(p)
	}
	return open(path)
}

func open(path string) (*Cache, error) {
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer connection keeps SQLite's locking simple; WAL lets reads
	// and the writer overlap within it.
	db.SetMaxOpenConns(1)
	c := &Cache{db: db}
	if err := c.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}

func (c *Cache) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := c.db.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM meta WHERE key='schema'`).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if v != 0 && v != schemaVersion {
		for _, t := range []string{"profiles", "sessions", "transcripts"} {
			if _, err := c.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+t); err != nil {
				return err
			}
		}
	}
	if _, err := c.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	_, err = c.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('schema',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(schemaVersion))
	return err
}

// Close closes the file.
func (c *Cache) Close() error { return c.db.Close() }

// Profiles returns an instance's agents in their stored order.
func (c *Cache) Profiles(ctx context.Context, instance string) ([]hermes.Profile, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT data FROM profiles WHERE instance=? ORDER BY ord`, instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hermes.Profile
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var p hermes.Profile
		if json.Unmarshal(data, &p) == nil {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// PutProfiles replaces an instance's agents.
func (c *Cache) PutProfiles(ctx context.Context, instance string, ps []hermes.Profile) error {
	return c.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM profiles WHERE instance=?`, instance); err != nil {
			return err
		}
		st, err := tx.PrepareContext(ctx, `INSERT INTO profiles(instance,name,ord,data) VALUES(?,?,?,?)`)
		if err != nil {
			return err
		}
		defer st.Close()
		for i, p := range ps {
			data, _ := json.Marshal(p)
			if _, err := st.ExecContext(ctx, instance, p.Name, i, data); err != nil {
				return err
			}
		}
		return nil
	})
}

// Sessions returns an instance's sessions, most recent first.
func (c *Cache) Sessions(ctx context.Context, instance string) ([]hermes.Session, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT data FROM sessions WHERE instance=? ORDER BY last_active DESC`, instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hermes.Session
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var s hermes.Session
		if json.Unmarshal(data, &s) == nil {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

// PutSessions stores sessions. With replace, the instance's other cached
// sessions are removed (a full listing came back without them).
func (c *Cache) PutSessions(ctx context.Context, instance string, ss []hermes.Session, replace bool) error {
	return c.tx(ctx, func(tx *sql.Tx) error {
		if replace {
			if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS keep(id TEXT PRIMARY KEY)`); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM temp.keep`); err != nil {
				return err
			}
		}
		up, err := tx.PrepareContext(ctx, `INSERT INTO sessions(instance,id,profile,last_active,data) VALUES(?,?,?,?,?)
			ON CONFLICT(instance,id) DO UPDATE SET profile=excluded.profile, last_active=excluded.last_active, data=excluded.data`)
		if err != nil {
			return err
		}
		defer up.Close()
		var keep *sql.Stmt
		if replace {
			if keep, err = tx.PrepareContext(ctx, `INSERT OR IGNORE INTO temp.keep(id) VALUES(?)`); err != nil {
				return err
			}
			defer keep.Close()
		}
		for _, s := range ss {
			data, _ := json.Marshal(s)
			if _, err := up.ExecContext(ctx, instance, s.ID, s.Profile, s.Activity().Unix(), data); err != nil {
				return err
			}
			if keep != nil {
				if _, err := keep.ExecContext(ctx, s.ID); err != nil {
					return err
				}
			}
		}
		if replace {
			if _, err := tx.ExecContext(ctx, `DELETE FROM transcripts WHERE instance=? AND session NOT IN (SELECT id FROM temp.keep)`, instance); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE instance=? AND id NOT IN (SELECT id FROM temp.keep)`, instance); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteSession forgets a session and its transcript.
func (c *Cache) DeleteSession(ctx context.Context, instance, id string) error {
	return c.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE instance=? AND id=?`, instance, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM transcripts WHERE instance=? AND session=?`, instance, id)
		return err
	})
}

// Transcript returns a cached transcript and when it was fetched; ok is
// false when none is cached.
func (c *Cache) Transcript(ctx context.Context, instance, session string) (msgs []hermes.Message, fetched time.Time, ok bool, err error) {
	var data []byte
	var at float64
	err = c.db.QueryRowContext(ctx, `SELECT data, fetched_at FROM transcripts WHERE instance=? AND session=?`, instance, session).Scan(&data, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, time.Time{}, false, nil // unreadable: treat as missing
	}
	return msgs, hermes.UnixTime(at).Time, true, nil
}

// PutTranscript stores a session's transcript.
func (c *Cache) PutTranscript(ctx context.Context, instance, session string, msgs []hermes.Message, fetched time.Time) error {
	data, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	_, err = c.db.ExecContext(ctx, `INSERT INTO transcripts(instance,session,fetched_at,data) VALUES(?,?,?,?)
		ON CONFLICT(instance,session) DO UPDATE SET fetched_at=excluded.fetched_at, data=excluded.data`,
		instance, session, float64(fetched.UnixNano())/1e9, data)
	return err
}

// ForgetInstance removes everything cached for an instance.
func (c *Cache) ForgetInstance(ctx context.Context, instance string) error {
	return c.tx(ctx, func(tx *sql.Tx) error {
		for _, t := range []string{"profiles", "sessions", "transcripts"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+t+" WHERE instance=?", instance); err != nil {
				return err
			}
		}
		return nil
	})
}

func (c *Cache) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Meta reads a value kept beside the data ("" when unset).
func (c *Cache) Meta(ctx context.Context, key string) string {
	var v string
	c.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	return v
}

// SetMeta keeps a value beside the data.
func (c *Cache) SetMeta(ctx context.Context, key, value string) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
