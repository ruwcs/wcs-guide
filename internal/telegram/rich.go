package telegram

import (
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/ruwcs/wcs-guide/internal/content"
)

// richNodes преобразует блоки RichMessage в сохраняемое представление публикации.
// Поддерживаются абзацы, заголовки, списки, цитаты и блоки кода.
// Медиа и остальные виды блоков не поддерживаются.
func richNodes(blocks []tg.PageBlockClass, depth int) ([]Node, error) {
	if depth > content.MaxDepth {
		return nil, fmt.Errorf("telegram: rich depth limit")
	}
	var nodes []Node
	for _, block := range blocks {
		node := Node{Kind: block.TypeName()}
		switch value := block.(type) {
		case *tg.PageBlockList:
			for _, item := range value.Items {
				child := Node{Kind: "list_item", Text: "• "}
				switch item := item.(type) {
				case *tg.PageListItemText:
					if item.Checkbox {
						child.Text = "☐ "
						if item.Checked {
							child.Text = "☑ "
						}
					}
					text, err := richText(item.Text, depth+1)
					if err != nil {
						return nil, err
					}
					child.Children = []Node{text}
				case *tg.PageListItemBlocks:
					if item.Checkbox {
						child.Text = "☐ "
						if item.Checked {
							child.Text = "☑ "
						}
					}
					children, err := richNodes(item.Blocks, depth+1)
					if err != nil {
						return nil, err
					}
					child.Children = children
				default:
					return nil, fmt.Errorf("telegram: unsupported list item")
				}
				node.Children = append(node.Children, child)
			}
		case *tg.PageBlockOrderedList:
			if value.Type != "" && value.Type != "1" {
				return nil, fmt.Errorf("telegram: unsupported ordered list style")
			}
			for index, item := range value.Items {
				number := value.Start + index
				if value.Reversed {
					number = value.Start - index
				}
				if number <= 0 {
					number = index + 1
				}
				child := Node{Kind: "list_item", Text: fmt.Sprintf("%d. ", number)}
				switch item := item.(type) {
				case *tg.PageListOrderedItemText:
					if item.Num != "" {
						child.Text = item.Num + ". "
					}
					if item.Checkbox {
						child.Text += "☐ "
						if item.Checked {
							child.Text = child.Text[:len(child.Text)-len("☐ ")] + "☑ "
						}
					}
					text, err := richText(item.Text, depth+1)
					if err != nil {
						return nil, err
					}
					child.Children = []Node{text}
				case *tg.PageListOrderedItemBlocks:
					if item.Num != "" {
						child.Text = item.Num + ". "
					}
					if item.Checkbox {
						child.Text += "☐ "
						if item.Checked {
							child.Text = child.Text[:len(child.Text)-len("☐ ")] + "☑ "
						}
					}
					blocks, err := richNodes(item.Blocks, depth+1)
					if err != nil {
						return nil, err
					}
					child.Children = blocks
				default:
					return nil, fmt.Errorf("telegram: unsupported ordered item")
				}
				node.Children = append(node.Children, child)
			}
		case *tg.PageBlockBlockquote:
			first, err := richText(value.Text, depth+1)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, first)
			if value.Caption != nil {
				caption, err := richText(value.Caption, depth+1)
				if err != nil {
					return nil, err
				}
				node.Children = append(node.Children, Node{Kind: "caption", Children: []Node{caption}})
			}
		case *tg.PageBlockPreformatted:
			node.Language = value.Language
			text, err := richText(value.Text, depth+1)
			if err != nil {
				return nil, err
			}
			node.Children = []Node{text}
		default:
			switch block.(type) {
			case *tg.PageBlockParagraph, *tg.PageBlockHeading1, *tg.PageBlockHeading2, *tg.PageBlockHeading3, *tg.PageBlockHeading4, *tg.PageBlockHeading5, *tg.PageBlockHeading6:
				value := block.(richTextBlock)
				text, err := richText(value.GetText(), depth+1)
				if err != nil {
					return nil, err
				}
				node.Children = []Node{text}
			default:
				return nil, fmt.Errorf("telegram: unsupported rich block %s", block.TypeName())
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// richText преобразует форматированный текст Telegram в сохраняемое представление.
func richText(value tg.RichTextClass, depth int) (Node, error) {
	if depth > content.MaxDepth {
		return Node{}, fmt.Errorf("telegram: rich depth limit")
	}
	if value == nil {
		return Node{Kind: "textEmpty"}, nil
	}
	node := Node{Kind: value.TypeName()}
	switch value := value.(type) {
	case *tg.TextEmpty:
	case *tg.TextPlain:
		node.Text = value.Text
	case *tg.TextCustomEmoji:
		node.Text = value.Alt
		node.DocumentID = value.DocumentID
	case *tg.TextConcat:
		for _, item := range value.Texts {
			child, err := richText(item, depth+1)
			if err != nil {
				return Node{}, err
			}
			node.Children = append(node.Children, child)
		}
	default:
		switch value.(type) {
		case *tg.TextBold, *tg.TextItalic, *tg.TextUnderline, *tg.TextStrike, *tg.TextFixed, *tg.TextURL, *tg.TextSpoiler:
			child, err := richText(value.(richTextBlock).GetText(), depth+1)
			if err != nil {
				return Node{}, err
			}
			node.Children = []Node{child}
			if link, ok := value.(*tg.TextURL); ok {
				node.URL = link.URL
			}
		default:
			return Node{}, fmt.Errorf("telegram: unsupported rich text %s", value.TypeName())
		}
	}
	return node, nil
}

// richDocument преобразует блоки RichMessage в форматированный документ.
func richDocument(nodes []Node) (content.Document, error) {
	var out strings.Builder
	var spans []content.Span
	var walk func(Node, int) error
	walk = func(node Node, depth int) error {
		if depth > content.MaxDepth || out.Len() > content.MaxText {
			return fmt.Errorf("telegram: rich limits")
		}
		start := out.Len()
		kind := content.SpanKind("")
		block := strings.HasPrefix(node.Kind, "pageBlock")
		switch node.Kind {
		case "textPlain", "textCustomEmoji":
			out.WriteString(node.Text)
			if node.Kind == "textCustomEmoji" {
				kind = content.CustomEmoji
			}
		case "list_item":
			out.WriteString(node.Text)
		case "textBold":
			kind = content.Bold
		case "textItalic":
			kind = content.Italic
		case "textUnderline":
			kind = content.Underline
		case "textStrike":
			kind = content.Strike
		case "textFixed":
			kind = content.Code
		case "textUrl":
			kind = content.Link
		case "textSpoiler":
			kind = content.Spoiler
		case "pageBlockBlockquote":
			kind = content.Quote
		case "pageBlockPreformatted":
			kind = content.CodeBlock
		case "pageBlockHeading1", "pageBlockHeading2", "pageBlockHeading3", "pageBlockHeading4", "pageBlockHeading5", "pageBlockHeading6":
			kind = content.Heading
		case "textEmpty", "textConcat", "pageBlockParagraph", "pageBlockList", "pageBlockOrderedList", "caption":
		default:
			return fmt.Errorf("telegram: unsupported stored rich node")
		}
		if node.Kind == "caption" {
			out.WriteByte('\n')
		}
		for _, child := range node.Children {
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}
		if kind != "" && out.Len() > start {
			spans = append(spans, content.Span{Start: start, End: out.Len(), Kind: kind, URL: node.URL})
		}
		if block {
			out.WriteString("\n\n")
		} else if node.Kind == "list_item" {
			out.WriteByte('\n')
		}
		return nil
	}
	for _, node := range nodes {
		if err := walk(node, 0); err != nil {
			return content.Document{}, err
		}
	}
	return content.Normalize(out.String(), spans)
}
