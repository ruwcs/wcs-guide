package domain

import (
	"strings"
	"unicode/utf8"
)

// TextAnchor — текстовый якорь границы извлечения.
// Пустой Text означает отсутствие границы; Include тогда игнорируется.
type TextAnchor struct {
	Text    string // Точная подстрока якоря; пусто — границы нет.
	Include bool   // Включение всей строки с якорем в результат.
}

// ExtractionRules — правила извлечения полезного фрагмента.
type ExtractionRules struct {
	Start TextAnchor // Начальный якорь полезного фрагмента.
	End   TextAnchor // Конечный якорь полезного фрагмента.
}

// Validate проверяет правила извлечения.
func (r ExtractionRules) Validate() error {
	for _, a := range []TextAnchor{r.Start, r.End} {
		if !utf8.ValidString(a.Text) || strings.ContainsAny(a.Text, "\r\n") || (a.Text != "" && blank(a.Text)) {
			return invalid("ExtractionRules.anchor")
		}
	}
	return nil
}
