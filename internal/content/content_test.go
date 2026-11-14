package content

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ruwcs/wcs-guide/internal/domain"
	"github.com/yuin/goldmark/ast"
)

func TestExtract(t *testing.T) {
	text := "📅 Save the dates!\r\n\r\n  полезное 雪\r\n\r\nСм также: ссылка\r\n"
	cases := []struct {
		rules domain.ExtractionRules // Границы выделения.
		want  string                 // Ожидаемый полезный текст.
		err   error                  // Ожидаемая причина отказа.
	}{
		{domain.ExtractionRules{}, text, nil},
		{domain.ExtractionRules{Start: domain.TextAnchor{Text: "Save"}, End: domain.TextAnchor{Text: "См также:"}}, "  полезное 雪\r\n", nil},
		{domain.ExtractionRules{Start: domain.TextAnchor{Text: "Save", Include: true}, End: domain.TextAnchor{Text: "См также:", Include: true}}, text, nil},
		{domain.ExtractionRules{Start: domain.TextAnchor{Text: "нет"}}, "", ErrAnchorNotFound},
		{domain.ExtractionRules{Start: domain.TextAnchor{Text: "См также:"}, End: domain.TextAnchor{Text: "Save"}}, "", ErrAnchorOrder},
		{domain.ExtractionRules{Start: domain.TextAnchor{Text: "Save"}, End: domain.TextAnchor{Text: "dates"}}, "", ErrAnchorOrder},
	}
	for _, tc := range cases {
		got, err := Extract(Document{Text: text}, tc.rules)
		if !errors.Is(err, tc.err) || got.Text != tc.want {
			t.Fatalf("получено %q, %v", got.Text, err)
		}
	}
	for _, text := range []string{"", " \t\n"} {
		_, err := Extract(Document{Text: text}, domain.ExtractionRules{})
		if !errors.Is(err, ErrEmptyResult) {
			t.Fatal(err)
		}
	}
	_, err := Extract(Document{Text: "aaaa"}, domain.ExtractionRules{Start: domain.TextAnchor{Text: "aaa"}})
	if !errors.Is(err, ErrAnchorAmbiguous) {
		t.Fatal(err)
	}
}
func TestRangesAndImmutability(t *testing.T) {
	input := Document{Text: "начало\nтекст\nконец", Spans: []Span{{Start: 0, End: len("начало\nтекст\nконец"), Kind: Link, URL: "https://example.org"}}}
	before := append([]Span(nil), input.Spans...)
	got, err := Extract(input, domain.ExtractionRules{Start: domain.TextAnchor{Text: "начало"}, End: domain.TextAnchor{Text: "конец"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spans[0].Start != 0 || got.Spans[0].End != len(got.Text) {
		t.Fatal(got)
	}
	got.Spans[0].URL = "changed"
	if !reflect.DeepEqual(before, input.Spans) {
		t.Fatal("изменён вход")
	}
	for _, span := range []Span{{Start: 1, End: 3, Kind: Bold}, {Start: -1, End: 2, Kind: Bold}, {Start: 0, End: 100, Kind: Bold}} {
		if _, err := Normalize("雪", []Span{span}); !errors.Is(err, ErrInvalidDocument) {
			t.Fatal(err)
		}
	}
	if _, err := Normalize(string([]byte{255}), nil); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal(err)
	}
	if _, err := Normalize(strings.Repeat("x", MaxText+1), nil); !errors.Is(err, ErrLimitExceeded) {
		t.Fatal(err)
	}
}
func TestMarkdown(t *testing.T) {
	cases := []struct {
		doc        Document // Исходные текст и оформление.
		want       string   // Ожидаемый Markdown.
		diagnostic bool     // Необходимость диагностики.
	}{
		{Document{Text: "<script>*x*</script>"}, "\\<script\\>\\*x\\*\\<\\/script\\>", false},
		{Document{Text: "ссылка", Spans: []Span{{Start: 0, End: 12, Kind: Link, URL: "javascript:alert(1)"}}}, "ссылка", true},
		{Document{Text: "секрет", Spans: []Span{{Start: 0, End: 12, Kind: Spoiler}, {Start: 0, End: 12, Kind: Link, URL: "https://secret.example"}}}, "\\[скрытый фрагмент\\]", true},
		{Document{Text: "text", Spans: []Span{{Start: 0, End: 4, Kind: Bold}, {Start: 0, End: 4, Kind: Italic}}}, "***text***", false},
		{Document{Text: "a`b", Spans: []Span{{Start: 0, End: 3, Kind: Code}}}, "``a`b``", false},
		{Document{Text: "abc", Spans: []Span{{Start: 0, End: 3, Kind: Underline}}}, "abc", true},
		{Document{Text: "first\nsecond"}, "first\\\nsecond", false},
	}
	for _, tc := range cases {
		result, err := ToMarkdown(tc.doc)
		if err != nil || result.Text != tc.want || (len(result.Diagnostics) > 0) != tc.diagnostic {
			t.Fatalf("получено %q, %v, %v", result.Text, result.Diagnostics, err)
		}
	}
	result, err := ToMarkdown(Document{Text: "a b c", Spans: []Span{{Start: 0, End: 3, Kind: Bold}, {Start: 2, End: 5, Kind: Italic}}})
	if err != nil || result.Text != "a b c" || len(result.Diagnostics) == 0 {
		t.Fatal(result, err)
	}
	_, err = ToMarkdown(Document{Text: "abc", Spans: []Span{{Start: 0, End: 2, Kind: Link, URL: "https://example.org/a"}, {Start: 1, End: 3, Kind: Link, URL: "https://example.org/b"}}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

// utf16Bytes переводит тестовые координаты без Telegram SDK.
func utf16Bytes(text string, offset, length int) (int, int, error) {
	positions := []int{0}
	for pos, r := range text {
		if r > 0xffff {
			positions = append(positions, -1)
		}
		positions = append(positions, pos+utf8.RuneLen(r))
	}
	if offset < 0 || length < 0 || offset >= len(positions) || length > len(positions)-1-offset || positions[offset] < 0 || positions[offset+length] < 0 {
		return 0, 0, ErrInvalidDocument
	}
	return positions[offset], positions[offset+length], nil
}
func TestUTF16(t *testing.T) {
	text := "А😀🇰🇷👩‍💻е́雪"
	start, end, err := utf16Bytes(text, 1, 2)
	if err != nil || text[start:end] != "😀" {
		t.Fatal(start, end, err)
	}
	if _, _, err := utf16Bytes(text, 2, 1); err == nil {
		t.Fatal("принята половина суррогатной пары")
	}
}
func TestExamples(t *testing.T) {
	for _, name := range []string{"calendar", "directory"} {
		raw, err := os.ReadFile("testdata/" + name + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		start, end := "Save the dates!", "См также:"
		label := "Just IMPRO"
		include := false
		if name == "directory" {
			start, end, label, include = "🇷🇺 Россия", "Как пользоваться", "Ивара", true
		}
		pos := strings.Index(text, label)
		spans := []Span{{Start: pos, End: pos + len(label), Kind: Link, URL: "https://example.org/" + name}}
		labels := []string{"The Open", "BudaFest", "Korean Open"}
		if name == "directory" {
			labels = []string{"Sugar Party", "MMDance", "YouDance", "Импульс", "WCS Yerevan", "WCS in Minsk", "A-Class"}
			for _, country := range []string{"🇷🇺 Россия", "🇦🇲 Армения", "🇧🇾 Беларусь"} {
				pos := strings.Index(text, country)
				spans = append(spans, Span{Start: pos, End: pos + len(country), Kind: Bold})
			}
		}
		for _, label := range labels {
			pos := strings.Index(text, label)
			spans = append(spans, Span{Start: pos, End: pos + len(label), Kind: Link, URL: "https://example.org/" + name})
		}
		document, err := Normalize(text, spans)
		if err != nil {
			t.Fatal(err)
		}
		fragment, err := Extract(document, domain.ExtractionRules{Start: domain.TextAnchor{Text: start, Include: include}, End: domain.TextAnchor{Text: end}})
		if err != nil {
			t.Fatal(err)
		}
		root, source, _ := parseMarkdown(t, fragment)
		links := findNodes(root, ast.KindLink)
		if len(links) != len(labels)+1 {
			t.Fatal("потеряны ссылки")
		}
		expectedLabels := append([]string{label}, labels...)
		for index, node := range links {
			if visible(node, source) != expectedLabels[index] {
				t.Fatal("изменён порядок или подпись ссылки")
			}
		}
		if name == "directory" && len(findNodes(root, ast.KindEmphasis)) != 3 {
			t.Fatal("потеряно оформление стран")
		}
		decoded := visible(root, source)
		if strings.Contains(decoded, "Save the dates!") || strings.Contains(decoded, "Как пользоваться") || strings.Contains(decoded, "См также:") {
			t.Fatal("утечка навигации")
		}
		result, err := ToMarkdown(fragment)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := os.ReadFile("testdata/" + name + ".md")
		if err != nil {
			t.Fatal(err)
		}
		if result.Text != string(expected) {
			t.Fatalf("%s: %q", name, result.Text)
		}
		repeated, err := ToMarkdown(fragment)
		if err != nil || !reflect.DeepEqual(result, repeated) {
			t.Fatal("недетерминированный результат")
		}
	}
}
func FuzzExtract(f *testing.F) {
	f.Add("😀\nначало\ntext\nконец", 0, 4)
	f.Fuzz(func(t *testing.T, text string, start, end int) {
		input := Document{Text: text, Spans: []Span{{Start: start, End: end, Kind: Bold}}}
		before := append([]Span(nil), input.Spans...)
		result, err := Extract(input, domain.ExtractionRules{})
		if err == nil {
			if _, err := Normalize(result.Text, result.Spans); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(before, input.Spans) {
			t.Fatal("изменён вход")
		}
	})
}
func FuzzMarkdown(f *testing.F) {
	f.Add("a😀b", 0, 1, "https://example.org")
	f.Add("a\n\nb", 0, 4, "https://example.org")
	f.Add("a\r\n \t\r\nb", 0, 9, "https://example.org")
	f.Fuzz(func(t *testing.T, text string, start, end int, target string) {
		input := Document{Text: text, Spans: []Span{{Start: start, End: end, Kind: Link, URL: target}}}
		before := append([]Span(nil), input.Spans...)
		result, err := ToMarkdown(input)
		if err == nil && (!utf8.ValidString(result.Text) || len(result.Text) > MaxMarkdown) {
			t.Fatal("нарушен контракт")
		}
		if !reflect.DeepEqual(before, input.Spans) {
			t.Fatal("изменён вход")
		}
	})
}

func TestBlocksAndSafeDestinations(t *testing.T) {
	for _, kind := range []SpanKind{CodeBlock, Quote} {
		result, err := ToMarkdown(Document{Text: "<script>\nvalue\n", Spans: []Span{{Start: 0, End: 15, Kind: kind}}})
		if err != nil || strings.Contains(result.Text, "javascript:") {
			t.Fatal(result, err)
		}
	}
	for _, target := range []string{"data:text/html,evil", "https://user:password@example.org", "https://example.org/a b", "https://example.org/\\evil"} {
		result, err := ToMarkdown(Document{Text: "label", Spans: []Span{{Start: 0, End: 5, Kind: Link, URL: target}}})
		if err != nil || result.Text != "label" || len(result.Diagnostics) == 0 {
			t.Fatal(result, err)
		}
	}
	result, err := ToMarkdown(Document{Text: "label", Spans: []Span{{Start: 0, End: 5, Kind: Code}, {Start: 1, End: 4, Kind: Link, URL: "https://example.org"}}})
	if err != nil || !strings.Contains(result.Text, "https://example.org") {
		t.Fatal("потеряна ссылка", result, err)
	}
}
