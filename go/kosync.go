package main

// KOReader progress sync (koreader-sync-server API), on the same SQLite schema as server.mjs.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"time"

	_ "modernc.org/sqlite"
)

type kosyncDB struct{ db *sql.DB }

func openKosync(path string) (*kosyncDB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		return nil, err
	}
	_, err = db.Exec(`
  CREATE TABLE IF NOT EXISTS users (username TEXT PRIMARY KEY, userkey TEXT NOT NULL, created INTEGER NOT NULL);
  CREATE TABLE IF NOT EXISTS progress (
    username TEXT NOT NULL, document TEXT NOT NULL, progress TEXT, percentage REAL,
    device TEXT, device_id TEXT, timestamp INTEGER NOT NULL, PRIMARY KEY (username, document));
`)
	return &kosyncDB{db}, err
}

func keyHash(k string) string {
	h := sha256.Sum256([]byte(k))
	return hex.EncodeToString(h[:])
}

// sqlValue converts a column value the way better-sqlite3 hands it to JS.
func sqlValue(v any) any {
	switch v := v.(type) {
	case int64:
		return float64(v)
	case []byte:
		return string(v)
	}
	return v
}

func (k *kosyncDB) userKey(user string) (string, bool, error) {
	var key string
	err := k.db.QueryRow(`SELECT userkey FROM users WHERE username = ?`, user).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return key, err == nil, err
}

type progressRow struct {
	percentage any
	device     any
	timestamp  float64
}

// latest is the most recent progress per document across all users.
func (k *kosyncDB) latest() (map[string]progressRow, error) {
	rows, err := k.db.Query(`SELECT document, MAX(timestamp) AS timestamp, percentage, device, username FROM progress GROUP BY document`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]progressRow{}
	for rows.Next() {
		var doc, ts, pct, dev, user any
		if err := rows.Scan(&doc, &ts, &pct, &dev, &user); err != nil {
			return nil, err
		}
		out[jsString(sqlValue(doc))] = progressRow{sqlValue(pct), sqlValue(dev), toNumber(sqlValue(ts))}
	}
	return out, rows.Err()
}

var errBodyTooLarge = errors.New("body too large")

func readJSONBody(r *http.Request) (any, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	s := string(data)
	if utf16Len(s) > 65536 {
		return nil, errBodyTooLarge
	}
	if s == "" {
		s = "{}"
	}
	v, err := parseJSON([]byte(s))
	if err != nil {
		return newObj(), nil
	}
	return v, nil
}

func prop(v any, k string) any {
	if o, ok := v.(*Obj); ok && o.Has(k) {
		return o.Get(k)
	}
	return undefined
}

var reSyncDoc = regexp.MustCompile(`^/syncs/progress/([^/]+)$`)

func (s *server) kosync(w http.ResponseWriter, r *http.Request, sub string) error {
	j := func(code int, body any) error { return send(w, code, stringify(body), "application/json", nil) }
	k := s.kdb
	if sub == "/healthcheck" {
		return j(200, newObj("state", "OK"))
	}
	if sub == "/users/create" && r.Method == "POST" {
		b, err := readJSONBody(r)
		if err != nil {
			return err
		}
		if b == nil {
			return errors.New("Cannot destructure property 'username' of '(intermediate value)' as it is null.")
		}
		username, password := prop(b, "username"), prop(b, "password")
		if !s.cfg.registration {
			return j(402, newObj("code", 2005.0, "message", "Registration is disabled."))
		}
		name, isStr := username.(string)
		if !truthy(username) || !truthy(password) || !isStr {
			return j(403, newObj("code", 2003.0, "message", "Invalid request"))
		}
		_, exists, err := k.userKey(name)
		if err != nil {
			return err
		}
		if exists {
			return j(402, newObj("code", 2002.0, "message", "Username is already registered."))
		}
		if _, err := k.db.Exec(`INSERT INTO users (username, userkey, created) VALUES (?, ?, ?)`, name, keyHash(jsString(password)), time.Now().UnixMilli()); err != nil {
			return err
		}
		log.Printf("%s kosync user created %s", nowISO(), stringify(name))
		return j(201, newObj("username", name))
	}
	user := ""
	if u, key := headerJoin(r, "X-Auth-User"), headerJoin(r, "X-Auth-Key"); u != "" && key != "" {
		stored, ok, err := k.userKey(u)
		if err != nil {
			return err
		}
		if ok && stored == keyHash(key) {
			user = u
		}
	}
	if user == "" {
		return j(401, newObj("code", 2001.0, "message", "Unauthorized"))
	}
	if sub == "/users/auth" {
		return j(200, newObj("authorized", "OK"))
	}
	if sub == "/syncs/progress" && r.Method == "PUT" {
		b, err := readJSONBody(r)
		if err != nil {
			return err
		}
		if b == nil {
			return errors.New("Cannot read properties of null (reading 'document')")
		}
		doc, progress := prop(b, "document"), prop(b, "progress")
		if !truthy(doc) || progress == nil || progress == undefined {
			return j(403, newObj("code", 2003.0, "message", "Invalid request"))
		}
		ts := time.Now().Unix()
		pct := toNumber(prop(b, "percentage"))
		if math.IsNaN(pct) {
			pct = 0
		}
		dev := prop(b, "device")
		if !truthy(dev) {
			dev = ""
		}
		devID := prop(b, "device_id")
		if !truthy(devID) {
			devID = ""
		}
		_, err = k.db.Exec(`INSERT INTO progress (username, document, progress, percentage, device, device_id, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT (username, document) DO UPDATE SET progress = excluded.progress, percentage = excluded.percentage,
    device = excluded.device, device_id = excluded.device_id, timestamp = excluded.timestamp`,
			user, jsString(doc), jsString(progress), pct, jsString(dev), jsString(devID), ts)
		if err != nil {
			return err
		}
		return j(200, newObj("document", doc, "timestamp", float64(ts)))
	}
	if m := reSyncDoc.FindStringSubmatch(sub); m != nil && r.Method == "GET" {
		doc, err := decodeURIComponent(m[1])
		if err != nil {
			return err
		}
		var d, p, pct, dev, devID, ts any
		err = k.db.QueryRow(`SELECT document, progress, percentage, device, device_id, timestamp FROM progress WHERE username = ? AND document = ?`, user, doc).
			Scan(&d, &p, &pct, &dev, &devID, &ts)
		if errors.Is(err, sql.ErrNoRows) {
			return j(200, newObj())
		}
		if err != nil {
			return err
		}
		return j(200, newObj("document", sqlValue(d), "progress", sqlValue(p), "percentage", sqlValue(pct),
			"device", sqlValue(dev), "device_id", sqlValue(devID), "timestamp", sqlValue(ts)))
	}
	return j(404, newObj("code", 2000.0, "message", "Not found"))
}

// headerJoin reads a header the way Node's req.headers does (repeated values joined with ", ").
func headerJoin(r *http.Request, name string) string {
	vs := r.Header.Values(name)
	out := ""
	for i, v := range vs {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}
