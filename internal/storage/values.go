package storage

import (
	"database/sql"
	"encoding/json"
	"time"
)

// timeFormat сохраняет UTC с наносекундной точностью и лексикографическим порядком.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func stamp(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(timeFormat)
}
func parsed(s sql.NullString) (time.Time, error) {
	if !s.Valid {
		return time.Time{}, nil
	}
	return time.Parse(timeFormat, s.String)
}
func validTime(t time.Time) error {
	if !t.IsZero() && (t.UTC().Year() < 1 || t.UTC().Year() > 9999) {
		return invalid("time")
	}
	return nil
}
func jsonMap(m map[string]string) (any, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(m)
	return string(b), err
}
func parseMap(s sql.NullString) (map[string]string, error) {
	if !s.Valid {
		return nil, nil
	}
	var m map[string]string
	err := json.Unmarshal([]byte(s.String), &m)
	return m, err
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
