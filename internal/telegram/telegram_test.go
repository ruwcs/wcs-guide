package telegram

import (
	"strings"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/ruwcs/wcs-guide/internal/content"
)

// TestRichMessage проверяет преобразование текстового RichMessage.
func TestRichMessage(t *testing.T) {
	blocks := []tg.PageBlockClass{
		&tg.PageBlockHeading1{Text: &tg.TextPlain{Text: "Календарь 😀"}},
		&tg.PageBlockParagraph{Text: &tg.TextConcat{Texts: []tg.RichTextClass{&tg.TextPlain{Text: "Посетите "}, &tg.TextURL{Text: &tg.TextBold{Text: &tg.TextPlain{Text: "Школу"}}, URL: "https://example.org/school"}}}},
		&tg.PageBlockList{Items: []tg.PageListItemClass{&tg.PageListItemText{Text: &tg.TextPlain{Text: "🇷🇺 Москва"}}, &tg.PageListItemText{Text: &tg.TextSpoiler{Text: &tg.TextURL{Text: &tg.TextPlain{Text: "HIDDEN"}, URL: "https://hidden.example"}}}}},
	}
	nodes, err := richNodes(blocks, 0)
	if err != nil {
		t.Fatal(err)
	}
	original := Publication{Rich: nodes}
	document, err := original.Document()
	if err != nil {
		t.Fatal(err)
	}
	result, err := content.ToMarkdown(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Календарь", "😀", "https://example.org/school", "Москва", "•"} {
		if !strings.Contains(result.Text, expected) {
			t.Fatal("потеряно содержимое", expected)
		}
	}
	if strings.Contains(result.Text, "HIDDEN") || strings.Contains(result.Text, "hidden.example") {
		t.Fatal("раскрыт спойлер")
	}
	if _, err := richNodes([]tg.PageBlockClass{&tg.PageBlockUnsupported{}}, 0); err == nil {
		t.Fatal("потерян неподдерживаемый блок")
	}
}

// TestUTF16Boundaries проверяет диапазоны UTF-16.
func TestUTF16Boundaries(t *testing.T) {
	value := "Я😀🇷🇺é한"
	start, end, err := byteRange(value, 1, 2)
	if err != nil || value[start:end] != "😀" {
		t.Fatal(err)
	}
	if _, _, err := byteRange(value, 2, 1); err == nil {
		t.Fatal("принята половина пары")
	}

}

// TestInvalidOriginalUTF8 проверяет отклонение повреждённого исходного UTF-8.
func TestInvalidOriginalUTF8(t *testing.T) {
	original := Publication{Text: string([]byte{0xff})}
	if original.Validate() == nil {
		t.Fatal("повреждённый текст будет молча заменён JSON-кодировщиком")
	}
	original.Text = "Текст"
	original.Rich = []Node{{Kind: "textPlain", Text: string([]byte{0xff})}}
	if original.Validate() == nil {
		t.Fatal("принят повреждённый RichMessage")
	}
}

// TestPostReference проверяет разбор адреса Telegram-публикации.
func TestPostReference(t *testing.T) {
	for _, reference := range []string{"http://t.me/channel/1", "https://evil.example/channel/1", "https://t.me/channel/1?x=1", "https://t.me/channel/1#", "https://t.me/channel/0", "https://t.me/channel/01", "https://t.me/c/1/2", "https://t.me/channel%2fother/1"} {
		if _, _, err := ParsePost(reference); err == nil {
			t.Fatal("принят некорректный адрес")
		}
	}
	if name, id, err := ParsePost("https://t.me/channel_name/123"); err != nil || name != "channel_name" || id != 123 {
		t.Fatal("отклонён публичный адрес")
	}
}

// TestUnicodeRanges проверяет диапазоны форматирования Unicode-текста.
func TestUnicodeRanges(t *testing.T) {
	value := "Я😀🇷🇺é한"
	for _, item := range []struct {
		Offset int    // Начало UTF-16-диапазона.
		Length int    // Длина в единицах UTF-16.
		Text   string // Ожидаемый исходный фрагмент.
	}{{0, 1, "Я"}, {1, 2, "😀"}, {3, 4, "🇷🇺"}, {7, 2, "é"}, {9, 1, "한"}} {
		start, end, err := byteRange(value, item.Offset, item.Length)
		if err != nil || value[start:end] != item.Text {
			t.Fatal("повреждён Unicode-диапазон")
		}
	}
	for _, offset := range []int{-1, 2, 4, 6, 100} {
		if _, _, err := byteRange(value, offset, 1); err == nil {
			t.Fatal("принята некорректная граница")
		}
	}
}
