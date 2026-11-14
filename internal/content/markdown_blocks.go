package content

import "strings"

// renderSections отделяет блочные элементы и абзацы без построения дерева документа.
func renderSections(text string, spans []Span, inline func(int, int, []Span) (string, error), warn func(string, int)) (string, error) {
	project := func(start, end int, ranges []Span) []Span {
		var result []Span
		for _, span := range ranges {
			if span.Kind == Quote || span.Kind == CodeBlock {
				continue
			}
			span.Start = max(start, span.Start)
			span.End = min(end, span.End)
			if decorative(span.Kind) {
				if span.Start < span.End && !emphasisBoundary(text, span) {
					warn("formatting_simplified", -1)
					continue
				}
			}
			if span.Start < span.End {
				result = append(result, span)
			}
		}
		return result
	}
	paragraphs := func(start, end int, ranges []Span) (string, error) {
		var out strings.Builder
		cursor := start
		paragraph := start
		rangeCursor := 0
		var activeRanges []Span
		flush := func(stop int) error {
			if paragraph < stop {
				for rangeCursor < len(ranges) && ranges[rangeCursor].Start < stop {
					activeRanges = append(activeRanges, ranges[rangeCursor])
					rangeCursor++
				}
				active := activeRanges[:0]
				for _, span := range activeRanges {
					if span.End > paragraph {
						active = append(active, span)
					}
				}
				activeRanges = active
				body, err := inline(paragraph, stop, project(paragraph, stop, activeRanges))
				if err != nil {
					return err
				}
				out.WriteString(body)
			}
			if out.Len() > MaxMarkdown {
				return ErrLimitExceeded
			}
			return nil
		}
		for cursor < end {
			next := end
			if pos := strings.IndexByte(text[cursor:end], '\n'); pos >= 0 {
				next = cursor + pos + 1
			}
			if strings.Trim(text[cursor:next], " \t\r\n") == "" {
				stop := cursor
				if stop > paragraph && text[stop-1] == '\n' {
					stop--
					if stop > paragraph && text[stop-1] == '\r' {
						stop--
					}
				}
				if err := flush(stop); err != nil {
					return "", err
				}
				out.WriteString(strings.ReplaceAll(text[stop:next], "\r\n", "\n"))
				paragraph = next
			}
			cursor = next
		}
		if err := flush(end); err != nil {
			return "", err
		}
		return out.String(), nil
	}
	var scope func(int, int, []Span) (string, error)
	scope = func(start, end int, ranges []Span) (string, error) {
		var out strings.Builder
		cursor := start
		afterBlock := false
		separate := func() {
			if out.Len() == 0 {
				return
			}
			value := out.String()
			if !strings.HasSuffix(value, "\n") {
				out.WriteByte('\n')
			}
			if !strings.HasSuffix(out.String(), "\n\n") {
				out.WriteByte('\n')
			}
		}
		for index, span := range ranges {
			if span.Kind != Quote && span.Kind != CodeBlock {
				continue
			}
			if span.Start < cursor {
				continue
			}
			gapEnd := span.Start
			// Перенос рядом с блоком — структурный, а не hard break обычного текста.
			for gapEnd > cursor && (text[gapEnd-1] == '\r' || text[gapEnd-1] == '\n') {
				gapEnd--
			}
			gap, err := paragraphs(cursor, gapEnd, ranges)
			if err != nil {
				return "", err
			}
			if gap != "" {
				if afterBlock {
					separate()
				}
				out.WriteString(gap)
			}
			separate()
			var body string
			if span.Kind == CodeBlock {
				raw := strings.ReplaceAll(text[span.Start:span.End], "\r\n", "\n")
				fence := strings.Repeat("`", max(3, longestTicks(raw)+1))
				body = fence + "\n" + raw
				if !strings.HasSuffix(body, "\n") {
					body += "\n"
				}
				body += fence
			} else {
				var children []Span
				for childIndex, child := range ranges {
					if childIndex == index {
						continue
					}
					child.Start = max(child.Start, span.Start)
					child.End = min(child.End, span.End)
					if child.Start < child.End {
						children = append(children, child)
					}
				}
				body, err = scope(span.Start, span.End, children)
				if err != nil {
					return "", err
				}
				body = strings.TrimSuffix(body, "\n")
				body = "> " + strings.ReplaceAll(body, "\n", "\n> ")
			}
			out.WriteString(body)
			if out.Len() > MaxMarkdown {
				return "", ErrLimitExceeded
			}
			cursor = span.End
			for cursor < end && (text[cursor] == '\r' || text[cursor] == '\n') {
				cursor++
			}
			afterBlock = true
		}
		if cursor < end {
			body, err := paragraphs(cursor, end, ranges)
			if err != nil {
				return "", err
			}
			if body != "" {
				if afterBlock {
					separate()
				}
				out.WriteString(body)
			}
		}
		if out.Len() > MaxMarkdown {
			return "", ErrLimitExceeded
		}
		return out.String(), nil
	}
	return scope(0, len(text), spans)
}
