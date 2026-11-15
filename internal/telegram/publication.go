package telegram

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ruwcs/wcs-guide/internal/content"
)

// usernamePattern — шаблон публичного имени канала Telegram.
var usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)

// Entity — форматирование фрагмента публикации Telegram.
type Entity struct {
	Kind       string // Вид форматирования Telegram.
	Offset     int    // Начало в единицах UTF-16.
	Length     int    // Длина в единицах UTF-16.
	URL        string // Адрес текстовой гиперссылки.
	Language   string // Язык блока кода.
	DocumentID int64  // Идентификатор Custom Emoji.
	Collapsed  bool   // Исходный признак свёрнутой цитаты.
}

// Publication — исходное содержимое публикации Telegram.
type Publication struct {
	Rich      []Node   // Исходная структура текстового RichMessage.
	ChannelID int64    // Постоянный идентификатор канала.
	MessageID int      // Идентификатор сообщения внутри канала.
	Text      string   // Полный исходный текст до извлечения.
	Entities  []Entity // Исходные диапазоны форматирования.
	CreatedAt int      // Время создания в секундах Unix.
	EditedAt  int      // Время изменения в секундах Unix; ноль — неизвестно.
}

// ParsePost разбирает публичный адрес публикации Telegram.
func ParsePost(raw string) (string, int, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "t.me" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") || parsed.RawPath != "" {
		return "", 0, errors.New("требуется https://t.me/channel/message_id")
	}
	parts := strings.Split(parsed.Path, "/")
	if len(parts) != 3 || !usernamePattern.MatchString(parts[1]) {
		return "", 0, errors.New("недопустимый адрес публикации")
	}
	id, err := strconv.ParseInt(parts[2], 10, 32)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != parts[2] {
		return "", 0, errors.New("недопустимый message ID")
	}
	return parts[1], int(id), nil
}

// byteRange переводит диапазон UTF-16 в байтовые координаты UTF-8.
func byteRange(value string, offset, length int) (int, int, error) {
	if offset < 0 || length <= 0 || offset > len(value) || length > len(value) || offset > len(value)-length {
		return 0, 0, errors.New("некорректный диапазон UTF-16")
	}
	boundaries, err := utf16Boundaries(value)
	if err != nil {
		return 0, 0, err
	}

	start, startOK := boundaries[offset]
	end, endOK := boundaries[offset+length]
	if !startOK || !endOK {
		return 0, 0, errors.New("граница внутри суррогатной пары")
	}
	return start, end, nil
}

// utf16Boundaries сопоставляет позиции UTF-16 и UTF-8.
func utf16Boundaries(value string) (map[int]int, error) {
	if !utf8.ValidString(value) || len(value) > content.MaxText {
		return nil, fmt.Errorf("telegram: invalid text")
	}
	boundaries := map[int]int{0: 0}
	units := 0
	for position, char := range value {
		width := 1
		if char > 0xffff {
			width = 2
		}
		units += width
		boundaries[units] = position + utf8.RuneLen(char)
	}
	return boundaries, nil
}

// Validate проверяет исходное содержимое публикации.
func (p Publication) Validate() error {
	if !utf8.ValidString(p.Text) || len(p.Text) > content.MaxText || len(p.Entities) > content.MaxSpans {
		return fmt.Errorf("telegram: invalid original")
	}
	for _, item := range p.Entities {
		if !utf8.ValidString(item.Kind) || !utf8.ValidString(item.URL) || !utf8.ValidString(item.Language) {
			return fmt.Errorf("telegram: invalid entity text")
		}
	}
	nodes := 0
	size := len(p.Text)
	var walk func(Node, int) error
	walk = func(node Node, depth int) error {
		nodes++
		size += len(node.Text) + len(node.URL) + len(node.Language) + len(node.Kind)
		if size > content.MaxMarkdown {
			return fmt.Errorf("telegram: rich original too large")
		}
		if depth > content.MaxDepth || nodes > content.MaxSpans*4 || !utf8.ValidString(node.Kind) || !utf8.ValidString(node.Text) || !utf8.ValidString(node.URL) || !utf8.ValidString(node.Language) {
			return fmt.Errorf("telegram: invalid rich original")
		}
		for _, child := range node.Children {
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	for _, node := range p.Rich {
		if err := walk(node, 0); err != nil {
			return err
		}
	}
	return nil
}

// Node — элемент содержимого RichMessage.
type Node struct {
	Kind       string // Вид элемента Telegram.
	Text       string // Оригинальный текст или маркер элемента списка.
	URL        string // Адрес текстовой ссылки.
	Language   string // Исходное обозначение языка блока кода.
	DocumentID int64  // Идентификатор Custom Emoji.
	Children   []Node // Вложенные элементы в исходном порядке.
}
