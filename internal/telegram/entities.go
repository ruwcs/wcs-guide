package telegram

import (
	"fmt"

	"github.com/ruwcs/wcs-guide/internal/content"
)

// Document преобразует публикацию Telegram в форматированный текст.
func (p Publication) Document() (content.Document, error) {
	if len(p.Rich) > 0 {
		return richDocument(p.Rich)
	}
	boundaries, err := utf16Boundaries(p.Text)
	if err != nil {
		return content.Document{}, err
	}
	if len(p.Entities) > content.MaxSpans {
		return content.Document{}, content.ErrLimitExceeded
	}
	spans := make([]content.Span, 0, len(p.Entities))
	for _, item := range p.Entities {
		if item.Offset < 0 || item.Length <= 0 || item.Offset > len(p.Text) || item.Length > len(p.Text) || item.Offset > len(p.Text)-item.Length {
			return content.Document{}, fmt.Errorf("telegram: invalid entity range")
		}
		start, startOK := boundaries[item.Offset]
		end, endOK := boundaries[item.Offset+item.Length]
		if !startOK || !endOK {
			return content.Document{}, fmt.Errorf("telegram: invalid UTF-16 boundary")
		}
		var kind content.SpanKind
		switch item.Kind {
		case "messageEntityTextUrl", "messageEntityUrl":
			kind = content.Link
		case "messageEntityBold":
			kind = content.Bold
		case "messageEntityItalic":
			kind = content.Italic
		case "messageEntityStrike":
			kind = content.Strike
		case "messageEntityUnderline":
			kind = content.Underline
		case "messageEntityCode":
			kind = content.Code
		case "messageEntityPre":
			kind = content.CodeBlock
		case "messageEntityBlockquote":
			kind = content.Quote
		case "messageEntitySpoiler":
			kind = content.Spoiler
		case "messageEntityCustomEmoji":
			kind = content.CustomEmoji
		default:
			return content.Document{}, fmt.Errorf("неподдерживаемый вид entity: %s", item.Kind)
		}
		address := item.URL
		if item.Kind == "messageEntityUrl" {
			address = p.Text[start:end]
		}
		spans = append(spans, content.Span{Start: start, End: end, Kind: kind, URL: address})
	}
	return content.Normalize(p.Text, spans)
}
