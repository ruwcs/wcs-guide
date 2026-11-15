package web

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark/ast"
	extensionast "github.com/yuin/goldmark/extension/ast"
)

// exportMarkdown преобразует Markdown-дерево в текст Markdown.
func exportMarkdown(node ast.Node, source []byte) (string, error) {
	var children strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		value, err := exportMarkdown(child, source)
		if err != nil {
			return "", err
		}
		children.WriteString(value)
	}
	body := children.String()
	lines := func() string {
		var out strings.Builder
		for i := 0; i < node.Lines().Len(); i++ {
			segment := node.Lines().At(i)
			out.Write(segment.Value(source))
		}
		return out.String()
	}
	switch current := node.(type) {
	case *ast.LinkReferenceDefinition:
		return "", nil
	case *ast.Document:
		return body, nil
	case *ast.Paragraph:
		return body + "\n\n", nil
	case *ast.TextBlock:
		return body + "\n", nil
	case *ast.Text:
		value := escape(string(current.Value(source)))
		if current.HardLineBreak() {
			value += "\\\n"
		} else if current.SoftLineBreak() {
			value += "\n"
		}
		return value, nil
	case *ast.String:
		return escape(string(current.Value)), nil
	case *ast.Heading:
		return strings.Repeat("#", current.Level) + " " + body + "\n\n", nil
	case *ast.Emphasis:
		delimiter := strings.Repeat("*", current.Level)
		return delimiter + body + delimiter, nil
	case *extensionast.Strikethrough:
		return "~~" + body + "~~", nil
	case *ast.Link:
		destination := strings.NewReplacer("<", "%3C", ">", "%3E", "\n", "%0A").Replace(string(current.Destination))
		return "[" + body + "](<" + destination + ">)", nil
	case *ast.AutoLink:
		return "[" + escape(string(current.Label(source))) + "](<" + string(current.URL(source)) + ">)", nil
	case *ast.CodeSpan:
		var value strings.Builder
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			value.Write(child.(*ast.Text).Value(source))
		}
		code := strings.ReplaceAll(value.String(), "\n", " ")
		fence := fenceFor(code)
		return fence + " " + code + " " + fence, nil
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		code := lines()
		fence := fenceFor(code)
		if len(fence) < 3 {
			fence = "```"
		}
		info := ""
		if block, ok := node.(*ast.FencedCodeBlock); ok && block.Info != nil {
			info = string(block.Language(source))
			if strings.ContainsAny(info, "`\r\n") {
				info = ""
			}
		}
		return fence + info + "\n" + code + strings.Repeat("\n", boolInt(!strings.HasSuffix(code, "\n"))) + fence + "\n\n", nil
	case *ast.Blockquote:
		return prefixLines(strings.TrimRight(body, "\n"), "> ") + "\n\n", nil
	case *ast.List:
		return body + "\n", nil
	case *ast.ListItem:
		marker := "- "
		if list, ok := node.Parent().(*ast.List); ok && list.IsOrdered() {
			marker = fmt.Sprintf("%d. ", list.Start)
		}
		value := strings.TrimRight(body, "\n")
		parts := strings.Split(value, "\n")
		return marker + parts[0] + "\n" + continuation(parts[1:], len(marker)) + "\n", nil
	case *ast.ThematicBreak:
		return "---\n\n", nil
	default:
		return "", fmt.Errorf("web: неподдерживаемый узел Markdown: %s", node.Kind())
	}
}

// boolInt преобразует логическое значение в ноль или единицу.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// fenceFor возвращает разделитель блока кода.
func fenceFor(value string) string {
	longest, run := 0, 0
	for _, char := range value {
		if char == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", longest+1)
}

// prefixLines добавляет префикс к строкам текста.
func prefixLines(value, prefix string) string {
	return prefix + strings.ReplaceAll(value, "\n", "\n"+prefix)
}

// continuation формирует продолжение элемента списка.
func continuation(lines []string, indent int) string {
	if len(lines) == 0 {
		return ""
	}
	return prefixLines(strings.Join(lines, "\n"), strings.Repeat(" ", indent)) + "\n"
}
