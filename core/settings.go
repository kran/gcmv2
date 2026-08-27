package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/kran/dba"
)

// keyRe 配置键格式（字母/数字/下划线/连字符/点）。
var keyRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// checkKey 键格式校验。
func checkKey(key string) error {
	if !keyRe.MatchString(key) {
		return fmt.Errorf("settings: key %q must match %s", key, keyRe)
	}
	return nil
}

// Setting 站点配置项（key-value, value 为 JSON 解码值）。
type Setting struct {
	Key       string    `db:"key" json:"key"`
	Group     string    `db:"group_name" json:"group"`
	Type      string    `db:"type" json:"type"`
	RawValue  string    `db:"value" json:"-"`
	Value     any       `db:"-" json:"value"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// GetSetting 取一条; 未找到返回 (nil, nil)。
func (s *Service) GetSetting(key string) (*Setting, error) {
	st, err := s.db.Add(`SELECT * FROM settings WHERE "key" = #{1}`, key).FetchOne[Setting]()
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, nil
	}
	if st.RawValue != "" {
		_ = json.Unmarshal([]byte(st.RawValue), &st.Value)
	}
	return st, nil
}

// SetSetting 写配置（upsert）。
func (s *Service) SetSetting(key, group, typ string, value any) error {
	if err := checkKey(key); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.Add(`INSERT INTO settings ("key", "group_name", "type", "value", "updated_at")
		VALUES (#{1}, #{2}, #{3}, #{4}, datetime('now'))
		ON CONFLICT("key") DO UPDATE SET "group_name" = #{2}, "type" = #{3}, "value" = #{4}, updated_at = datetime('now')`,
		key, group, typ, string(b)).Exec()
	return err
}

var _ = dba.SQL{}

// GetSettingValue 取一条并 JSON 解码到 dest; 未找到返回 (false, nil)。
func (s *Service) GetSettingValue(key string, dest any) (bool, error) {
	st, err := s.GetSetting(key)
	if err != nil || st == nil {
		return false, err
	}
	raw := []byte(st.RawValue)
	if len(raw) == 0 {
		raw = []byte("null")
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, fmt.Errorf("settings: %s value invalid: %w", key, err)
	}
	return true, nil
}

// GetSettings 批量取（键列表, 缺失静默跳过）。
func (s *Service) GetSettings(keys []string) (map[string]Setting, error) {
	if len(keys) == 0 {
		return map[string]Setting{}, nil
	}
	q := s.db.Add(`SELECT * FROM settings WHERE key IN (#{1|expand})`, keys)
	rows, err := q.FetchList[Setting]()
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	out := map[string]Setting{}
	for _, r := range rows {
		if err := r.decode(); err != nil {
			return nil, fmt.Errorf("settings: %q: %w", r.Key, err)
		}
		out[r.Key] = r
	}
	return out, nil
}

// ListSettings 全部配置（可按分组过滤）。
func (s *Service) ListSettings(group string) ([]Setting, error) {
	q := s.db.Add(`SELECT * FROM settings`)
	if group != "" {
		q = q.Add(`WHERE group_name = #{1}`, group)
	}
	q = q.Add(`ORDER BY group_name, key`)
	rows, err := q.FetchList[Setting]()
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	for i := range rows {
		if err := rows[i].decode(); err != nil {
			return nil, fmt.Errorf("settings: %q: %w", rows[i].Key, err)
		}
	}
	return rows, nil
}

// DeleteSetting 删配置。
func (s *Service) DeleteSetting(key string) error {
	affected, err := s.db.Add(`DELETE FROM settings WHERE key = #{1}`, key).Exec()
	if err != nil {
		return err
	}
	n, _ := affected.RowsAffected()
	if n == 0 {
		return errors.New("settings: not found: " + key)
	}
	return nil
}

// decode value JSON → Value。
func (st *Setting) decode() error {
	if st.RawValue == "" || st.RawValue == "{}" {
		st.Value = nil
		return nil
	}
	return json.Unmarshal([]byte(st.RawValue), &st.Value)
}
