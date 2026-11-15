package content

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Diagnostic — сообщение об упрощении форматирования.
type Diagnostic struct {
	Code      string // Стабильная причина изменения.
	SpanIndex int    // Номер диапазона нормализованного входа; -1 для общей причины.
}

// MarkdownResult — результат преобразования документа в Markdown.
type MarkdownResult struct {
	Text        string       // Текст для последующего сохранения.
	Diagnostics []Diagnostic // Сообщения об упрощении оформления.
}

// ToMarkdown преобразует документ в Markdown.
func ToMarkdown(input Document) (MarkdownResult, error) {
	document, err := Normalize(input.Text, input.Spans)
	if err != nil {
		return MarkdownResult{}, err
	}
	result := MarkdownResult{}
	warn := func(code string, index int) {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: code, SpanIndex: index})
	}
	// Маска и таблица координат исключают скрытый текст до сериализации.
	hidden := make([]bool, len(document.Text))
	for _, span := range document.Spans {
		if span.Kind == Spoiler {
			for pos := span.Start; pos < span.End; pos++ {
				hidden[pos] = true
			}
		}
	}
	positions := make([]int, len(hidden)+1)
	counts := make([]int, len(hidden)+1)
	var redacted strings.Builder
	inside := false
	for pos := range hidden {
		positions[pos] = redacted.Len()
		counts[pos+1] = counts[pos]
		if hidden[pos] {
			counts[pos+1]++
			if !inside {
				redacted.WriteString("[скрытый фрагмент]")
			}
			inside = true
		} else {
			redacted.WriteByte(document.Text[pos])
			inside = false
		}
	}
	positions[len(hidden)] = redacted.Len()
	spans := make([]Span, 0, len(document.Spans))
	for index, span := range document.Spans {
		concealed := counts[span.End] != counts[span.Start]
		if span.Kind == Spoiler {
			warn("spoiler_redacted", index)
			continue
		}
		if concealed && span.Kind == Link {
			warn("hidden_link_removed", index)
			continue
		}
		span.Start = positions[span.Start]
		span.End = positions[span.End]
		if span.Start == span.End {
			continue
		}
		switch span.Kind {
		case Underline, CustomEmoji:
			warn("decoration_removed", index)
			continue
		case Heading:
			warn("heading_simplified", index)
			span.Kind = Bold
		case Link:
			target, ok := safeURL(span.URL)
			if !ok {
				warn("unsafe_link_removed", index)
				continue
			}
			span.URL = target
		}
		spans = append(spans, span)
	}
	text := redacted.String()
	// Объединение одного вида не требует попарного сравнения всех диапазонов.
	merged := make([]Span, 0, len(spans))
	last := map[SpanKind]int{}
	for _, span := range spans {
		if index, ok := last[span.Kind]; ok && (merged[index].End > span.Start || (merged[index].End == span.Start && span.Kind != Quote && span.Kind != CodeBlock)) {
			if span.Kind == Link && merged[index].End > span.Start && merged[index].URL != span.URL {
				return MarkdownResult{}, ErrUnsupported
			}
			if merged[index].URL == span.URL {
				merged[index].End = max(merged[index].End, span.End)
				continue
			}
		}
		last[span.Kind] = len(merged)
		merged = append(merged, span)
	}
	spans = merged
	priority := func(kind SpanKind) int {
		switch kind {
		case Quote:
			return 0
		case CodeBlock:
			return 1
		case Link:
			return 2
		case Bold:
			return 3
		case Italic:
			return 4
		case Strike:
			return 5
		default:
			return 6
		}
	}
	sort.SliceStable(spans, func(i, j int) bool {
		a, b := spans[i], spans[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End > b.End
		}
		return priority(a.Kind) < priority(b.Kind)
	})
	dropped := make([]bool, len(spans))
	active := []int{}
	for index, span := range spans {
		retained := active[:0]
		for _, previous := range active {
			other := spans[previous]
			if other.End < span.Start {
				continue
			}
			retained = append(retained, previous)
			if other.End == span.Start {
				if decorative(other.Kind) && decorative(span.Kind) {
					dropped[previous] = true
					dropped[index] = true
				}
				continue
			}
			if (other.Kind == CodeBlock && span.Kind == Link) || (other.Kind == Link && span.Kind == CodeBlock) {
				return MarkdownResult{}, ErrUnsupported
			}
			if other.Kind == Code && span.Kind == Link {
				dropped[previous] = true
			} else if other.Kind == Code || other.Kind == CodeBlock {
				dropped[index] = true
			}
			if span.End > other.End && other.Kind != Quote && span.Kind != Quote {
				if span.Kind != Link {
					dropped[index] = true
				}
				if other.Kind != Link {
					dropped[previous] = true
				}
			}
		}
		active = append(retained, index)
		if len(active) > MaxDepth {
			return MarkdownResult{}, ErrLimitExceeded
		}

		if span.Kind == Code && strings.ContainsAny(text[span.Start:span.End], "\r\n") {
			dropped[index] = true
		}
		if (span.Kind == Quote || span.Kind == CodeBlock) && !(lineStart(text, span.Start) && (span.End == len(text) || lineStart(text, span.End) || text[span.End] == '\r' || text[span.End] == '\n')) {
			dropped[index] = true
		}
	}
	kept := spans[:0]
	for index, span := range spans {
		if dropped[index] {
			warn("formatting_simplified", -1)
		} else {
			kept = append(kept, span)
		}
	}
	var renderInline func(int, int, []Span) (string, error)
	renderInline = func(start, end int, ranges []Span) (string, error) {
		var out strings.Builder
		cursor := start
		for index := 0; index < len(ranges); {
			span := ranges[index]
			out.WriteString(plain(text[cursor:span.Start], lineStart(text, cursor), span.Start < len(text) && text[span.Start] != '\n'))
			next := index + 1
			for next < len(ranges) && ranges[next].Start < span.End {
				next++
			}
			body, err := renderInline(span.Start, span.End, ranges[index+1:next])
			if err != nil {
				return "", err
			}
			switch span.Kind {
			case Bold:
				body = "**" + body + "**"
			case Italic:
				body = "*" + body + "*"
			case Strike:
				body = "~~" + body + "~~"
			case Link:
				body = "[" + body + "](<" + span.URL + ">)"
			case Code:
				raw := text[span.Start:span.End]
				fence := strings.Repeat("`", longestTicks(raw)+1)
				padding := ""
				if strings.HasPrefix(raw, "`") || strings.HasSuffix(raw, "`") || (strings.HasPrefix(raw, " ") && strings.HasSuffix(raw, " ") && !blank(raw)) {
					padding = " "
				}
				body = fence + padding + raw + padding + fence
			}
			out.WriteString(body)
			if out.Len() > MaxMarkdown {
				return "", ErrLimitExceeded
			}
			cursor = span.End
			index = next
		}
		out.WriteString(plain(text[cursor:end], lineStart(text, cursor), end < len(text) && text[end] != '\n'))
		if out.Len() > MaxMarkdown {
			return "", ErrLimitExceeded
		}
		return out.String(), nil
	}
	result.Text, err = renderSections(text, kept, renderInline, warn)

	if err != nil {
		return MarkdownResult{}, err
	}
	return result, nil
}

// decorative определяет декоративное форматирование.
func decorative(kind SpanKind) bool { return kind == Bold || kind == Italic || kind == Strike }

// lineStart проверяет начало строки.
func lineStart(text string, pos int) bool { return pos == 0 || text[pos-1] == '\n' }

// emphasisBoundary проверяет возможность выделения фрагмента в Markdown.
func emphasisBoundary(text string, span Span) bool {
	first, _ := utf8.DecodeRuneInString(text[span.Start:])
	last, _ := utf8.DecodeLastRuneInString(text[:span.End])
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return false
	}
	if span.Start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:span.Start])
		if !unicode.IsSpace(r) && !unicode.IsPunct(r) {
			return false
		}
	}
	if span.End < len(text) {
		r, _ := utf8.DecodeRuneInString(text[span.End:])
		if !unicode.IsSpace(r) && !unicode.IsPunct(r) {
			return false
		}
	}
	return true
}

// longestTicks возвращает длину самой длинной последовательности обратных кавычек.
func longestTicks(text string) int {
	best, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			best = max(best, run)
		} else {
			run = 0
		}
	}
	return best
}

// safeURL проверяет допустимость адреса ссылки.
func safeURL(raw string) (string, bool) {
	if !utf8.ValidString(raw) || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\\' }) {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || (!strings.EqualFold(parsed.Scheme, "https") && !strings.EqualFold(parsed.Scheme, "http")) {
		return "", false
	}
	return strings.NewReplacer("&", "&amp;", "<", "%3C", ">", "%3E").Replace(parsed.String()), true
}

// plain экранирует текст и сохраняет переносы строк.
func plain(text string, leading bool, continues bool) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out strings.Builder
	for pos, r := range text {
		if r == '\n' {
			if ((pos > 0 && text[pos-1] != '\n') || (pos == 0 && !leading)) && ((pos+1 < len(text) && text[pos+1] != '\n') || (pos+1 == len(text) && continues)) {
				out.WriteByte('\\')
			}
			out.WriteByte('\n')
			leading = true
			continue
		}
		if leading && (r == ' ' || r == '\t') {
			fmt.Fprintf(&out, "&#%d;", r)
			continue
		}
		leading = false
		if r < 128 && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}
