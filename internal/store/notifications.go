package store

import (
	"chatgpt-space-merge/internal/notify"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type NotificationLog struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Bot    string    `json:"bot"`
	Kind   string    `json:"kind"`
	Title  string    `json:"title"`
	Status string    `json:"status"`
	Error  string    `json:"error,omitempty"`
}

func (s *Store) initializeNotifications() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS notification_settings(id INTEGER PRIMARY KEY CHECK(id=1),sealed TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS notification_logs(id INTEGER PRIMARY KEY AUTOINCREMENT,profile TEXT NOT NULL);`)
	return err
}
func (s *Store) notificationSettingsLocked() (notify.Settings, error) {
	out := notify.Defaults()
	var sealed string
	err := s.db.QueryRow(`SELECT sealed FROM notification_settings WHERE id=1`).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	raw, err := s.decrypt(sealed)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}
func (s *Store) NotificationSettings() (notify.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notificationSettingsLocked()
}
func (s *Store) SaveNotificationSettings(in notify.Settings) (notify.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, err := s.notificationSettingsLocked()
	if err != nil {
		return in, err
	}
	in, err = notify.MergeSettings(in, previous)
	if err != nil {
		return in, err
	}
	sealed, err := s.encrypt(mustJSON(in))
	if err != nil {
		return in, err
	}
	_, err = s.db.Exec(`INSERT INTO notification_settings VALUES(1,?) ON CONFLICT(id) DO UPDATE SET sealed=excluded.sealed`, sealed)
	return in.Public(), err
}
func (s *Store) RecordNotification(entry NotificationLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO notification_logs(profile) VALUES(?)`, mustJSON(entry)); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM notification_logs WHERE id NOT IN (SELECT id FROM notification_logs ORDER BY id DESC LIMIT 200)`); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) NotificationLogs() ([]NotificationLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id,profile FROM notification_logs ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotificationLog{}
	for rows.Next() {
		var id int64
		var raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var v NotificationLog
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		v.ID = id
		out = append(out, v)
	}
	return out, rows.Err()
}
