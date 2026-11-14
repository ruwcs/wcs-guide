package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ruwcs/wcs-guide/internal/domain"
)

// Extract выделяет целые строки по уникальным якорям и пересчитывает диапазоны.
func Extract(input Document, rules domain.ExtractionRules) (Document, error) {
	document, err := Normalize(input.Text, input.Spans)
	if err != nil {
		return Document{}, err
	}
	if err = rules.Validate(); err != nil {
		return Document{}, err
	}
	start, end := 0, len(document.Text)
	startLine, endLine := -1, -1
	locate := func(anchor, side string) (int, int, error) {
		if len(anchor) > MaxText {
			return 0, 0, ErrLimitExceeded
		}
		pos := strings.Index(document.Text, anchor)
		if pos < 0 {
			return 0, 0, fmt.Errorf("%w: %s", ErrAnchorNotFound, side)
		}
		if strings.Contains(document.Text[pos+1:], anchor) {
			return 0, 0, fmt.Errorf("%w: %s", ErrAnchorAmbiguous, side)
		}
		left := strings.LastIndex(document.Text[:pos], "\n") + 1
		right := len(document.Text)
		if next := strings.IndexByte(document.Text[pos:], '\n'); next >= 0 {
			right = pos + next + 1
		}
		return left, right, nil
	}
	if rules.Start.Text != "" {
		left, right, err := locate(rules.Start.Text, "start")
		if err != nil {
			return Document{}, err
		}
		startLine = left
		start = right
		if rules.Start.Include {
			start = left
		}
	}
	if rules.End.Text != "" {
		left, right, err := locate(rules.End.Text, "end")
		if err != nil {
			return Document{}, err
		}
		endLine = left
		end = left
		if rules.End.Include {
			end = right
		}
	}
	if startLine >= 0 && endLine >= 0 && startLine >= endLine {
		return Document{}, ErrAnchorOrder
	}
	if start > end {
		return Document{}, ErrAnchorOrder
	}
	codeStarts, codeEnds := []int{}, []int{}
	for _, span := range document.Spans {
		if span.Kind == CodeBlock {
			codeStarts = append(codeStarts, span.Start)
			end := span.End
			if len(codeEnds) > 0 {
				end = max(end, codeEnds[len(codeEnds)-1])
			}
			codeEnds = append(codeEnds, end)
		}
	}
	protected := func(left, right int) bool {
		index := sort.SearchInts(codeStarts, right) - 1
		return index >= 0 && codeEnds[index] > left
	}

	if rules.Start.Text != "" || rules.End.Text != "" {
		for start < end {
			next := end
			if pos := strings.IndexByte(document.Text[start:end], '\n'); pos >= 0 {
				next = start + pos + 1
			}
			if strings.Trim(document.Text[start:next], " \t\r\n") != "" || protected(start, next) {
				break
			}
			start = next
		}
		for start < end {
			last := end
			if document.Text[last-1] == '\n' {
				last--
			}
			left := strings.LastIndexByte(document.Text[start:last], '\n') + start + 1
			if strings.Trim(document.Text[left:end], " \t\r\n") != "" || protected(left, end) {
				break
			}
			end = left
		}
	}
	if blank(document.Text[start:end]) {
		return Document{}, ErrEmptyResult
	}
	result := Document{Text: document.Text[start:end]}
	for _, span := range document.Spans {
		span.Start = max(start, span.Start)
		span.End = min(end, span.End)
		if span.Start < span.End {
			span.Start -= start
			span.End -= start
			result.Spans = append(result.Spans, span)
		}
	}
	return result, nil
}
