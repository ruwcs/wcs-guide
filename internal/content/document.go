// Package content извлекает форматированный текст и преобразует его в Markdown.
package content

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxText     = 1 << 20 // Максимальный размер входного текста в байтах.
	MaxSpans    = 4096    // Максимальное количество диапазонов.
	MaxDepth    = 32      // Максимальная глубина вложения.
	MaxMarkdown = 8 << 20 // Максимальный размер результата в байтах.
)

// SpanKind — вид форматирования текста.
type SpanKind string

const (
	Bold        SpanKind = "bold"         // Жирный текст.
	Italic      SpanKind = "italic"       // Курсив.
	Strike      SpanKind = "strike"       // Зачёркивание.
	Link        SpanKind = "link"         // Текстовая ссылка.
	Code        SpanKind = "code"         // Встроенный код.
	CodeBlock   SpanKind = "code_block"   // Блок кода.
	Quote       SpanKind = "quote"        // Цитата.
	Underline   SpanKind = "underline"    // Подчёркивание.
	Spoiler     SpanKind = "spoiler"      // Намеренно скрытый текст.
	CustomEmoji SpanKind = "custom_emoji" // Специальный эмодзи с текстовым эквивалентом.
	Heading     SpanKind = "heading"      // Явно заданный заголовок.
)

// Document — форматированный текст.
type Document struct {
	Text  string // Оригинальный текст UTF-8.
	Spans []Span // Диапазоны в байтовых координатах.
}

// Span — форматирование фрагмента текста.
// Диапазон [Start, End) задаётся в байтах по границам символов UTF-8.
type Span struct {
	Start int      // Начальная позиция в байтах.
	End   int      // Конечная позиция в байтах, не включённая в диапазон.
	Kind  SpanKind // Вид оформления.
	URL   string   // Адрес гиперссылки.
}

var (
	// ErrInvalidDocument — некорректный текст или диапазоны документа.
	ErrInvalidDocument = errors.New("content: некорректный документ")
	// ErrUnsupported — неподдерживаемое представление содержимого.
	ErrUnsupported = errors.New("content: неподдерживаемое представление")
	// ErrLimitExceeded — превышение допустимого объёма или сложности.
	ErrLimitExceeded = errors.New("content: превышено ограничение")
	// ErrAnchorNotFound — отсутствие заданного якоря в тексте.
	ErrAnchorNotFound = errors.New("content: якорь не найден")
	// ErrAnchorAmbiguous — несколько совпадений заданного якоря.
	ErrAnchorAmbiguous = errors.New("content: неоднозначный якорь")
	// ErrAnchorOrder — недопустимый порядок строк якорей.
	ErrAnchorOrder = errors.New("content: неверный порядок границ")
	// ErrEmptyResult — пустой результат извлечения.
	ErrEmptyResult = errors.New("content: пустой результат")
)

// Normalize создаёт документ из текста и диапазонов форматирования.
func Normalize(text string, spans []Span) (Document, error) {
	if len(text) > MaxText || len(spans) > MaxSpans {
		return Document{}, ErrLimitExceeded
	}
	if !utf8.ValidString(text) {
		return Document{}, ErrInvalidDocument
	}
	for pos, r := range text {
		if (unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r') || r == 127 || (r == '\r' && (pos+1 == len(text) || text[pos+1] != '\n')) {
			return Document{}, ErrInvalidDocument
		}
	}
	result := Document{Text: text, Spans: append([]Span(nil), spans...)}
	attributeBytes := 0
	for index, span := range result.Spans {
		if len(span.URL) > MaxText-attributeBytes {
			return Document{}, ErrLimitExceeded
		}
		attributeBytes += len(span.URL)
		if span.Start < 0 || span.End > len(text) || span.Start >= span.End || !boundary(text, span.Start) || !boundary(text, span.End) || (span.Kind != Link && span.URL != "") {
			return Document{}, fmt.Errorf("%w: span %d", ErrInvalidDocument, index)
		}
		switch span.Kind {
		case Bold, Italic, Strike, Link, Code, CodeBlock, Quote, Underline, Spoiler, CustomEmoji, Heading:
		default:
			return Document{}, ErrUnsupported
		}
	}
	sort.Slice(result.Spans, func(i, j int) bool {
		a, b := result.Spans[i], result.Spans[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End > b.End
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.URL < b.URL
	})
	unique := result.Spans[:0]
	for _, span := range result.Spans {
		if len(unique) == 0 || unique[len(unique)-1] != span {
			unique = append(unique, span)
		}
	}
	result.Spans = unique
	// Открытые диапазоны ограничены даже при пересечениях.
	ends := make([]int, 0, MaxDepth)
	for _, span := range unique {
		active := ends[:0]
		for _, end := range ends {
			if end > span.Start {
				active = append(active, end)
			}
		}
		ends = append(active, span.End)
		if len(ends) > MaxDepth {
			return Document{}, ErrLimitExceeded
		}
	}
	return result, nil
}

// boundary проверяет границу символа UTF-8.
func boundary(text string, pos int) bool { return pos == len(text) || utf8.RuneStart(text[pos]) }

// blank проверяет отсутствие непробельных символов.
func blank(text string) bool { return strings.TrimSpace(text) == "" }
