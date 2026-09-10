package store

import (
	"net/url"
	"os"
	"strconv"
	"strings"

	"wikiatlas/backend/internal/domain"
)

// 环境变量 → settings 表的一次性导入。
//
// 目标：配置只维护在设置页里（存 SQLite），.env 退化为"首次启动的种子"。
// 导入只做一次（记 env_imported 标记），之后设置页怎么改都不会被 env 覆盖；
// 需要重新采用 env 时，设置页有「从环境重新导入」按钮，对应 ReimportEnvSettings。

func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// buildSettingsFromEnv 从 WIKIATLAS_* 组装一份设置（只覆盖有值的字段）。
func buildSettingsFromEnv() (*domain.Settings, map[string]string) {
	s := domain.DefaultSettings()
	secrets := map[string]string{}

	if v := envFirst("WIKIATLAS_LLM_BASE_URL", "WIKIATLAS_LLM_ENDPOINT"); v != "" {
		s.LLM.Endpoint = v
	}
	if v := envFirst("WIKIATLAS_LLM_MODEL"); v != "" {
		s.LLM.Model = v
	}
	if v := envFirst("WIKIATLAS_LLM_API_KEY", "OPENAI_API_KEY"); v != "" {
		secrets["llm_api_key"] = v
	}
	if v := envFirst("WIKIATLAS_LLM_TEMPERATURE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			s.LLM.Temperature = &f
		}
	}
	if v := envFirst("WIKIATLAS_LLM_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.LLM.MaxTokens = &n
		}
	}
	if v := envFirst("WIKIATLAS_EXA_API_KEY", "EXA_API_KEY"); v != "" {
		secrets["exa_api_key"] = v
	}
	if v := envFirst("WIKIATLAS_EMBY_URL"); v != "" {
		s.Library.EmbyURL = &v
	}
	if v := envFirst("WIKIATLAS_EMBY_API_KEY"); v != "" {
		s.Library.EmbyAPIKey = &v
	}
	if v := envFirst("WIKIATLAS_KOMGA_URL"); v != "" {
		s.Library.KomgaURL = &v
	}
	if v := envFirst("WIKIATLAS_KOMGA_API_KEY"); v != "" {
		s.Library.KomgaAPIKey = &v
	}
	if v := envFirst("WIKIATLAS_GAMEATLAS_URL"); v != "" {
		s.Library.GameAtlasURL = &v
	}
	if v := envFirst("WIKIATLAS_GAMEATLAS_API_KEY"); v != "" {
		s.Library.GameAtlasAPIKey = &v
	}
	if v := envFirst("WIKIATLAS_MAX_CONCURRENT_RUNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 16 {
			s.Runs.MaxConcurrentRuns = n
		}
	}
	if v := envFirst("WIKIATLAS_SKILL_ROOT"); v != "" {
		s.SkillRoot = v
	}
	return &s, secrets
}

// ImportEnvSettings 首次启动时把 env 写成 SQL 配置（幂等：已导入过就跳过）。
func (s *Store) ImportEnvSettings() (bool, error) {
	var raw string
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = 'env_imported'`).Scan(&raw)
	if err == nil && raw == "1" {
		return false, nil
	}
	return s.applyEnvSettings()
}

// ReimportEnvSettings 强制用 env 覆盖当前设置（设置页的「从环境重新导入」）。
func (s *Store) ReimportEnvSettings() (bool, error) {
	return s.applyEnvSettings()
}

func (s *Store) applyEnvSettings() (bool, error) {
	cfg, secrets := buildSettingsFromEnv()
	if err := s.SaveSettings(cfg); err != nil {
		return false, err
	}
	for key, val := range secrets {
		if err := s.SetSecret(key, val); err != nil {
			return false, err
		}
	}
	if _, err := s.DB.Exec(`INSERT INTO settings (key, value) VALUES ('env_imported', '1')
		ON CONFLICT(key) DO UPDATE SET value = '1'`); err != nil {
		return false, err
	}
	return true, nil
}

// SetSecret 写一个密钥类配置（llm_api_key / exa_api_key）。空值表示删除。
func (s *Store) SetSecret(key, value string) error {
	if strings.TrimSpace(value) == "" {
		_, err := s.DB.Exec(`DELETE FROM settings WHERE key = ?`, key)
		return err
	}
	_, err := s.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// GetSecret 读密钥类配置（空表示未配置）。
func (s *Store) GetSecret(key string) string {
	var raw string
	if err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw); err != nil {
		return ""
	}
	return raw
}

// ExaAPIKey 返回 Exa key（设置页优先，env 兜底）。
func (s *Store) ExaAPIKey() string {
	if v := s.GetSecret("exa_api_key"); v != "" {
		return v
	}
	return envFirst("WIKIATLAS_EXA_API_KEY", "EXA_API_KEY")
}

// MaskURL 只保留 host，用于日志/展示（避免把密钥拼进日志）。
func MaskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}
