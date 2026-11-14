package content

import (
	"bufio"
	"bytes"
	stdhtml "html"
	"reflect"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

func parseMarkdown(t *testing.T, doc Document) (ast.Node, []byte, string) {
	t.Helper()
	result, err := ToMarkdown(doc)
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(result.Text)
	parser := goldmark.New(goldmark.WithExtensions(extension.Strikethrough))
	root := parser.Parser().Parse(text.NewReader(source))
	var rendered bytes.Buffer
	if err := parser.Renderer().Render(&rendered, source, root); err != nil {
		t.Fatal(err)
	}
	return root, source, rendered.String()
}
func visible(node ast.Node, source []byte) string {
	var out bytes.Buffer
	writer := bufio.NewWriter(&out)
	ast.Walk(node, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch child := child.(type) {
		case *ast.Text:
			if child.IsRaw() {
				html.DefaultWriter.RawWrite(writer, child.Segment.Value(source))
			} else {
				html.DefaultWriter.Write(writer, child.Segment.Value(source))
			}
			if child.HardLineBreak() || child.SoftLineBreak() {
				writer.WriteByte('\n')
			}
		case *ast.String:
			html.DefaultWriter.Write(writer, child.Value)
		}
		return ast.WalkContinue, nil
	})
	writer.Flush()
	return stdhtml.UnescapeString(out.String())
}
func findNodes(root ast.Node, kind ast.NodeKind) []ast.Node {
	var nodes []ast.Node
	ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && node.Kind() == kind {
			nodes = append(nodes, node)
		}
		return ast.WalkContinue, nil
	})
	return nodes
}
func TestMarkdownParagraphSemantics(t *testing.T) {
	for _, separator := range []string{"\n\n", "\r\n\r\n", "\n \t\n"} {
		value := "Первая часть" + separator + "Вторая часть"
		for _, kind := range []SpanKind{Link, Bold, Italic, Strike} {
			span := Span{Start: 0, End: len(value), Kind: kind}
			if kind == Link {
				span.URL = "https://example.org/a?x=1&y=2"
			}
			root, source, _ := parseMarkdown(t, Document{Text: value, Spans: []Span{span}})
			paragraphs := findNodes(root, ast.KindParagraph)
			if len(paragraphs) != 2 {
				t.Fatalf("%s: абзацев %d", kind, len(paragraphs))
			}
			if visible(paragraphs[0], source) != "Первая часть" || visible(paragraphs[1], source) != "Вторая часть" {
				t.Fatal("изменён текст")
			}

			if kind == Link {
				links := findNodes(root, ast.KindLink)
				if len(links) != 2 {
					t.Fatalf("ссылок %d", len(links))
				}
				for _, node := range links {
					if stdhtml.UnescapeString(string(node.(*ast.Link).Destination)) != span.URL {
						t.Fatal("изменён адрес")
					}
				}
			}
			if kind == Strike && len(findNodes(root, extensionast.KindStrikethrough)) != 2 {
				t.Fatal("потеряно зачёркивание")
			}
			if kind == Bold || kind == Italic {
				if len(findNodes(root, ast.KindEmphasis)) != 2 {
					t.Fatal("потеряно оформление")
				}
			}
		}
	}
}
func TestMarkdownBlockBoundaries(t *testing.T) {
	for _, kind := range []SpanKind{Quote, CodeBlock} {
		for _, includeNewline := range []bool{false, true} {
			value := "Введение\nЦитата\nЗаключение"
			start := len("Введение\n")
			end := start + len("Цитата")
			if includeNewline {
				end++
			}
			root, source, _ := parseMarkdown(t, Document{Text: value, Spans: []Span{{Start: start, End: end, Kind: kind}}})
			if root.ChildCount() != 3 {
				t.Fatalf("%s: блоков %d", kind, root.ChildCount())
			}
			if visible(root.FirstChild(), source) != "Введение" || visible(root.LastChild(), source) != "Заключение" {
				t.Fatal("изменён обычный текст")
			}
			expected := ast.KindBlockquote
			if kind == CodeBlock {
				expected = ast.KindFencedCodeBlock
			}
			if root.FirstChild().NextSibling().Kind() != expected {
				t.Fatal("потерян блок")
			}
		}
	}
}
func TestMarkdownSafetyAndText(t *testing.T) {
	for _, value := range []string{"雪 👩‍💻 е́ 🇰🇷", "[a]*b*_c_`d` & <script>alert(1)</script>"} {
		root, source, rendered := parseMarkdown(t, Document{Text: value})
		if visible(root, source) != value {
			t.Fatal("изменён текст", rendered)
		}
		if len(findNodes(root, ast.KindRawHTML))+len(findNodes(root, ast.KindHTMLBlock)) != 0 {
			t.Fatal("сырой HTML")
		}
	}
	doc := Document{Text: "секрет", Spans: []Span{{Start: 0, End: 12, Kind: Spoiler}, {Start: 0, End: 12, Kind: Link, URL: "https://secret.example"}}}
	before := append([]Span(nil), doc.Spans...)
	root, source, rendered := parseMarkdown(t, doc)
	if strings.Contains(string(source), "секрет") || strings.Contains(rendered, "секрет") || strings.Contains(rendered, "secret.example") || len(findNodes(root, ast.KindLink)) != 0 {
		t.Fatal("раскрыт спойлер")
	}
	if !reflect.DeepEqual(before, doc.Spans) {
		t.Fatal("изменён вход")
	}
}

func TestMarkdownNestedAndSequentialBlocks(t *testing.T) {
	value := "first\nsecond"
	root, _, _ := parseMarkdown(t, Document{Text: value, Spans: []Span{{Start: 0, End: len(value), Kind: Link, URL: "https://example.org/a_(b)?q=x&y=z"}, {Start: 0, End: 5, Kind: Bold}, {Start: 6, End: 12, Kind: Italic}}})
	if len(findNodes(root, ast.KindLink)) != 1 || len(findNodes(root, ast.KindEmphasis)) != 2 {
		t.Fatal("потеряно вложенное оформление")
	}
	value = "first\nsecond"
	root, _, _ = parseMarkdown(t, Document{Text: value, Spans: []Span{{Start: 0, End: 6, Kind: Quote}, {Start: 6, End: 12, Kind: Quote}}})
	if len(findNodes(root, ast.KindBlockquote)) != 2 {
		t.Fatal("склеены цитаты")
	}
	value = "a\n\nb"
	root, _, rendered := parseMarkdown(t, Document{Text: value, Spans: []Span{{Start: 0, End: len(value), Kind: Quote}, {Start: 0, End: len(value), Kind: Link, URL: "https://example.org"}}})
	if len(findNodes(root, ast.KindBlockquote)) != 1 || len(findNodes(root, ast.KindLink)) != 2 {
		t.Fatal(rendered)
	}
	value = "a\n```\ncode\n```\nz"
	root, source, _ := parseMarkdown(t, Document{Text: value, Spans: []Span{{Start: 2, End: len(value) - 2, Kind: CodeBlock}}})
	if root.ChildCount() != 3 || visible(root.LastChild(), source) != "z" || len(findNodes(root, ast.KindFencedCodeBlock)) != 1 {
		t.Fatal("потеряны границы кода")
	}
}

func TestMarkdownNestedParagraphsAndUnsafeLinks(t *testing.T) {
	value := "Первый абзац\n\nВторой абзац"
	root, _, _ := parseMarkdown(t, Document{Text: value, Spans: []Span{{Start: 0, End: len(value), Kind: Link, URL: "https://example.org"}, {Start: 0, End: len(value), Kind: Bold}}})
	if len(findNodes(root, ast.KindLink)) != 2 || len(findNodes(root, ast.KindEmphasis)) != 2 {
		t.Fatal("потеряно вложение через абзацы")
	}
	for _, target := range []string{"javascript:alert(1)", "data:text/html,secret"} {
		root, source, rendered := parseMarkdown(t, Document{Text: "label", Spans: []Span{{Start: 0, End: 5, Kind: Link, URL: target}}})
		if len(findNodes(root, ast.KindLink)) != 0 || visible(root, source) != "label" || strings.Contains(rendered, target) {
			t.Fatal("опасная ссылка")
		}
	}
}
