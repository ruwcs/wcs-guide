package web

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/ruwcs/wcs-guide/internal/content"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// maxPageBytes — максимальный размер Markdown-содержимого страницы в байтах.
const maxPageBytes = 32 << 20

// maxASTDepth — максимальная глубина дерева Markdown.
const maxASTDepth = 128

// safeURL проверяет допустимость адреса ссылки.
func safeURL(value string) bool {
	if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) || r == '\\' }) || strings.HasPrefix(value, "//") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Opaque != "" {
		return false
	}
	if parsed.Scheme == "" {
		return parsed.Host == ""
	}
	return (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) && parsed.Hostname() != ""
}

// escape экранирует обычный текст Markdown.
func escape(value string) string {
	var out strings.Builder
	for _, char := range value {
		if strings.ContainsRune("\\`*_{}[]<>()#+-.!|~>", char) {
			out.WriteByte('\\')
		}
		out.WriteRune(char)
	}
	return out.String()
}

// renderMarkdown формирует безопасные HTML и Markdown блока.
func renderMarkdown(value string) (string, string, error) {
	if len(value) > content.MaxMarkdown {
		return "", "", fmt.Errorf("web: превышен размер блока")
	}
	parser := goldmark.New(goldmark.WithExtensions(extension.Strikethrough))
	source := []byte(value)
	root := parser.Parser().Parse(text.NewReader(source))
	var clean func(ast.Node, int) error
	clean = func(parent ast.Node, depth int) error {
		if depth > maxASTDepth {
			return fmt.Errorf("web: превышена вложенность")
		}
		for node := parent.FirstChild(); node != nil; {
			next := node.NextSibling()
			if err := clean(node, depth+1); err != nil {
				return err
			}
			switch current := node.(type) {
			case *ast.Image:
				parent.ReplaceChild(parent, node, ast.NewString(node.Text(source)))
			case *ast.Link:
				if !safeURL(string(current.Destination)) {
					for node.FirstChild() != nil {
						parent.InsertBefore(parent, node, node.FirstChild())
					}
					parent.RemoveChild(parent, node)
				}
			case *ast.AutoLink:
				if !safeURL(string(current.URL(source))) {
					parent.ReplaceChild(parent, node, ast.NewString(current.Label(source)))
				}
			case *ast.RawHTML:
				var raw bytes.Buffer
				for i := 0; i < current.Segments.Len(); i++ {
					segment := current.Segments.At(i)
					raw.Write(segment.Value(source))
				}
				parent.ReplaceChild(parent, node, ast.NewString(raw.Bytes()))
			case *ast.HTMLBlock:
				var raw bytes.Buffer
				for i := 0; i < current.Lines().Len(); i++ {
					segment := current.Lines().At(i)
					raw.Write(segment.Value(source))
				}
				if current.HasClosure() {
					raw.Write(current.ClosureLine.Value(source))
				}
				paragraph := ast.NewParagraph()
				paragraph.AppendChild(paragraph, ast.NewString(raw.Bytes()))
				parent.ReplaceChild(parent, node, paragraph)
			}
			node = next
		}
		return nil
	}
	if err := clean(root, 0); err != nil {
		return "", "", err
	}
	var html bytes.Buffer
	if err := parser.Renderer().Render(&html, source, root); err != nil {
		return "", "", err
	}
	markdown, err := exportMarkdown(root, source)
	return html.String(), markdown, err
}
