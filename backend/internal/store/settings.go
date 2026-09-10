package store

import (
	"database/sql"
	"encoding/json"

	"wikiatlas/backend/internal/domain"
)

// GetSettings loads settings from the settings key-value table, merging defaults.
func (s *Store) GetSettings() (*domain.Settings, error) {
	cfg := domain.DefaultSettings()
	var raw sql.NullString
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = 'app'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return &cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if raw.Valid && raw.String != "" {
		// overlay stored JSON onto defaults (partial merge)
		var stored domain.Settings
		if err := json.Unmarshal([]byte(raw.String), &stored); err == nil {
			mergeSettings(&cfg, &stored)
		}
	}
	// API key presence from env is not reflected here; handler may override.
	return &cfg, nil
}

// SaveSettings persists the full settings object.
func (s *Store) SaveSettings(st *domain.Settings) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO settings (key, value) VALUES ('app', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(b))
	return err
}

func mergeSettings(dst, src *domain.Settings) {
	if src.LLM.Endpoint != "" {
		dst.LLM.Endpoint = src.LLM.Endpoint
	}
	if src.LLM.Model != "" {
		dst.LLM.Model = src.LLM.Model
	}
	dst.LLM.APIKeyConfigured = src.LLM.APIKeyConfigured
	if src.LLM.Temperature != nil {
		dst.LLM.Temperature = src.LLM.Temperature
	}
	if src.LLM.MaxTokens != nil {
		dst.LLM.MaxTokens = src.LLM.MaxTokens
	}
	if src.Library.EmbyURL != nil {
		dst.Library.EmbyURL = src.Library.EmbyURL
	}
	if src.Library.EmbyAPIKey != nil {
		dst.Library.EmbyAPIKey = src.Library.EmbyAPIKey
	}
	if src.Library.KomgaURL != nil {
		dst.Library.KomgaURL = src.Library.KomgaURL
	}
	if src.Library.KomgaAPIKey != nil {
		dst.Library.KomgaAPIKey = src.Library.KomgaAPIKey
	}
	if src.Library.GameAtlasURL != nil {
		dst.Library.GameAtlasURL = src.Library.GameAtlasURL
	}
	if src.Library.GameAtlasAPIKey != nil {
		dst.Library.GameAtlasAPIKey = src.Library.GameAtlasAPIKey
	}
	if src.Runs.ExpireDays > 0 {
		dst.Runs.ExpireDays = src.Runs.ExpireDays
	}
	if src.Runs.KeepEventsDays > 0 {
		dst.Runs.KeepEventsDays = src.Runs.KeepEventsDays
	}
	if src.SkillRoot != "" {
		dst.SkillRoot = src.SkillRoot
	}
}

// HasAPIKey reports whether an API key is stored (never returns the key).
func (s *Store) HasAPIKey() bool {
	var raw sql.NullString
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = 'llm_api_key'`).Scan(&raw)
	if err != nil || !raw.Valid {
		return false
	}
	return raw.String != ""
}

// SetAPIKey stores the LLM API key (dev convenience; production should use a secret store).
func (s *Store) SetAPIKey(key string) error {
	if key == "" {
		_, err := s.DB.Exec(`DELETE FROM settings WHERE key = 'llm_api_key'`)
		return err
	}
	_, err := s.DB.Exec(`INSERT INTO settings (key, value) VALUES ('llm_api_key', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key)
	return err
}

// GetAPIKey returns the stored key or empty.
func (s *Store) GetAPIKey() string {
	var raw sql.NullString
	_ = s.DB.QueryRow(`SELECT value FROM settings WHERE key = 'llm_api_key'`).Scan(&raw)
	if raw.Valid {
		return raw.String
	}
	return ""
}
