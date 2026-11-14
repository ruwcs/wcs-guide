package domain

import (
	"strings"
	"unicode/utf8"
)

// TextAnchor задаёт точную подстроку и включение всей содержащей её строки.
// Пустой Text означает отсутствие границы; Include тогда игнорируется.
type TextAnchor struct {
	Text    string // Точная подстрока якоря; пусто — границы нет.
	Include bool   // Включение всей строки с якорем в результат.
}

// ExtractionRules задаёт границы извлечения без реализации поиска.
type ExtractionRules struct {
	Start TextAnchor // Начальный якорь полезного фрагмента.
	End   TextAnchor // Конечный якорь полезного фрагмента.
}

// Validate проверяет сами якоря; наличие и порядок проверит экстрактор.
func (r ExtractionRules) Validate() error {
	for _, a := range []TextAnchor{r.Start, r.End} {
		if !utf8.ValidString(a.Text) || strings.ContainsAny(a.Text, "\r\n") || (a.Text != "" && blank(a.Text)) {
			return invalid("ExtractionRules.anchor")
		}
	}
	return nil
}
