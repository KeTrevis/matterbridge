package gateway

import (
	"database/sql"
	"errors"
	"strings"

	_ "modernc.org/sqlite" // Pure Go SQLite driver, also used by the WhatsApp bridge.
)

type messageStore struct {
	db *sql.DB
}

func (gw *Gateway) rememberMessage(source string, ids []*BrMsgID) error {
	if gw.Messages.Contains(source) {
		return nil
	}
	if gw.Router.messageStore != nil && len(ids) > 0 {
		if err := gw.Router.messageStore.save(gw.Name, source, ids); err != nil {
			return err
		}
	}
	gw.Messages.Add(source, ids)
	return nil
}

func openMessageStore(path string) (*messageStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		PRAGMA busy_timeout = 5000;
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = FULL;
		CREATE TABLE IF NOT EXISTS message_aliases (
			gateway TEXT NOT NULL, id TEXT NOT NULL, canonical TEXT NOT NULL,
			PRIMARY KEY (gateway, id)
		);
		CREATE TABLE IF NOT EXISTS message_links (
			gateway TEXT NOT NULL, canonical TEXT NOT NULL, account TEXT NOT NULL,
			channel TEXT NOT NULL, destination TEXT NOT NULL,
			PRIMARY KEY (gateway, canonical, account, channel)
		);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &messageStore{db: db}, nil
}

func (s *messageStore) save(gateway, source string, ids []*BrMsgID) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // Also covers partial writes; Commit makes this a no-op.
	if _, err = tx.Exec(`INSERT OR IGNORE INTO message_aliases VALUES (?, ?, ?)`, gateway, source, source); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO message_links VALUES (?, ?, ?, ?, ?)`,
			gateway, source, id.br.Account, id.ChannelID, strings.TrimPrefix(id.ID, id.br.Protocol+" ")); err != nil {
			return err
		}
		aliases := []string{id.ID}
		if id.br.Protocol == "discord" {
			for _, part := range strings.Split(strings.TrimPrefix(id.ID, "discord "), ";") {
				if part != "" {
					aliases = append(aliases, "discord "+part)
				}
			}
		}
		for _, alias := range aliases {
			if _, err = tx.Exec(`INSERT OR IGNORE INTO message_aliases VALUES (?, ?, ?)`, gateway, alias, source); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *messageStore) canonical(gateway, id string) (string, error) {
	return messageID(s.db.QueryRow(`SELECT canonical FROM message_aliases WHERE gateway = ? AND id = ?`, gateway, id))
}

func (s *messageStore) destination(gateway, canonical, account, channel string) (string, error) {
	return messageID(s.db.QueryRow(`SELECT destination FROM message_links
		WHERE gateway = ? AND canonical = ? AND account = ? AND channel = ?`, gateway, canonical, account, channel))
}

func messageID(row *sql.Row) (string, error) {
	var id string
	err := row.Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
