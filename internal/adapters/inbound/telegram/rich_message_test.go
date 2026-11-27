package telegram

import (
	"net/http"
	"strings"
	"testing"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestPrepareRichMessage(test *testing.T) {
	test.Run("should preserve safe table content and omit hidden or unsafe content when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<table><tbody><tr><td><strong>Caption</strong></td><td>Value &amp; more</td></tr><tr><td></td><td><em>Explanation</em></td></tr><tr><td>Documents</td><td><div>First</div><hr><div>Second</div><p>Third</p><script>secret</script></td></tr></tbody></table><p hidden>hidden</p><a href="javascript:alert(1)">safe label</a><a href="https://example.com/item">source</a>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.True(test, strings.Contains(prepared.HTML, `<table><tr><td><strong>Caption</strong></td><td>Value &amp; more</td></tr>`), "adapted HTML should retain native table rows")
		assert.True(test, strings.Contains(prepared.HTML, `<td>First<br>────<br>Second<br>Third</td>`), "cell blocks should be flattened with inline breaks")
		assert.True(test, !strings.Contains(prepared.HTML, "<td><p>"), "paragraphs should not remain block wrappers inside cells")
		assert.True(test, strings.Contains(prepared.HTML, `<td></td>`), "empty table cells should be retained")
		assert.True(test, !strings.Contains(prepared.HTML, "secret") && !strings.Contains(prepared.PlainText, "secret"), "active content should be omitted")
		assert.True(test, !strings.Contains(prepared.PlainText, "hidden"), "hidden content should be omitted")
		assert.True(test, strings.Contains(prepared.HTML, "safe label"), "unsafe links should retain their visible label")
		assert.True(test, !strings.Contains(prepared.HTML, "javascript:"), "unsafe link targets should be removed")
		assert.True(test, strings.Contains(prepared.HTML, `<a href="https://example.com/item">source</a>`), "safe absolute links should be retained")
	})

	test.Run("should select complete plain text when table structure cannot be represented when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<table><tr><td>one</td><td>two</td></tr><tr><td>three</td></tr></table>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "", prepared.HTML)
		assert.Equal(test, "one | two\nthree", prepared.PlainText)
	})
}

func TestSplitPlainText(test *testing.T) {
	test.Run("should preserve every Unicode character within message limits when splitting long fallback text", func(test *testing.T) {
		// Given
		text := strings.Repeat("界🙂 ", 3000)

		// When
		chunks := splitPlainText(text)

		// Then
		assert.True(test, len(chunks) > 1, "long text should use multiple messages")
		for _, chunk := range chunks {
			assert.True(test, unicodeCount(chunk) <= plainTextLimit, "each message should fit Telegram's text limit")
		}
		assert.Equal(test, text, strings.Join(chunks, ""), "chunk boundaries should not lose or repeat content")
	})
}

func TestBotSendRich(test *testing.T) {
	test.Run("should send adapted HTML through Telegram rich message API when preparation succeeds", func(test *testing.T) {
		// Given
		environment := setup(test)
		fragment := `<h2>Summary</h2><table><tr><td>Key</td><td>Value</td></tr></table>`

		// When
		err := environment.bot.sendRich(test.Context(), 101, fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, 1, len(environment.richMessages), "valid rich HTML should use one rich send")
		assert.True(test, environment.skipEntityDetection, "rich delivery should disable automatic entity detection")
		assert.True(test, strings.Contains(environment.richMessages[0], `<table><tr><td>Key</td><td>Value</td></tr></table>`), "request should contain adapted HTML")
		assert.Equal(test, 0, len(environment.replies), "rich send should not also use classic text delivery")
	})

	test.Run("should send the complete text fallback without a rich attempt when a table is irregular", func(test *testing.T) {
		// Given
		environment := setup(test)
		fragment := `<table><tr><td>one</td><td>two</td></tr><tr><td>three</td></tr></table>`

		// When
		err := environment.bot.sendRich(test.Context(), 101, fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, 0, environment.richCalls, "unsupported table shape should fall back before sending")
		assert.Equal(test, 1, len(environment.replies))
		assert.Equal(test, "one | two\nthree", environment.replies[0])
	})

	test.Run("should not resend as text after an unverified rich rejection", func(test *testing.T) {
		// Given
		environment := setup(test)
		environment.richStatus = http.StatusBadRequest

		// When
		err := environment.bot.sendRich(test.Context(), 101, `<p>Readable</p>`)

		// Then
		assert.True(test, err != nil, "unverified markup rejection should fail")
		assert.Equal(test, 1, environment.richCalls, "unverified rejection should make one rich request")
		assert.Equal(test, 0, len(environment.replies), "unverified rejection must not trigger a text resend")
	})

	test.Run("should block subsequent delivery after an unauthorized rich response", func(test *testing.T) {
		// Given
		environment := setup(test)
		environment.richStatus = http.StatusUnauthorized

		// When
		firstErr := environment.bot.sendRich(test.Context(), 101, `<p>Readable</p>`)
		environment.richStatus = http.StatusOK
		secondErr := environment.bot.sendRich(test.Context(), 101, `<p>Readable</p>`)

		// Then
		assert.ErrorIs(test, firstErr, ErrInvalidToken)
		assert.ErrorIs(test, secondErr, ErrInvalidToken)
		assert.Equal(test, 1, environment.richCalls, "latched invalid token should stop future rich requests")
	})
}
