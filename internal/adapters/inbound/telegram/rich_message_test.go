package telegram

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestPrepareRichMessage(test *testing.T) {
	test.Run("should omit document metadata from rich and plain output when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<head><title>Internal portal title</title><meta name="description" content="secret metadata"><base href="https://example.com"><link rel="stylesheet" href="secret.css"></head><p>Visible content</p>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "<p>Visible content</p>", prepared.HTML)
		assert.Equal(test, "Visible content", prepared.PlainText)
		for _, metadata := range []string{"Internal portal title", "secret metadata", "secret.css"} {
			assert.True(test, !strings.Contains(prepared.HTML, metadata) && !strings.Contains(prepared.PlainText, metadata), "document metadata should be omitted from both outputs")
		}
	})

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

	test.Run("should preserve boundaries between block containers when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<div>First</div><div>Second</div><section>Third</section><article>Fourth</article>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "First<br>Second<br>Third<br>Fourth", prepared.HTML)
		assert.Equal(test, "First\nSecond\nThird\nFourth", prepared.PlainText)
	})

	test.Run("should insert a boundary before a block wrapper after inline text when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `prefix<div>block</div>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "prefix<br>block", prepared.HTML)
		assert.Equal(test, "prefix\nblock", prepared.PlainText)
	})

	test.Run("should insert a boundary before a block wrapper after inline span content when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<span>prefix</span><div>block</div>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "prefix<br>block", prepared.HTML)
		assert.Equal(test, "prefix\nblock", prepared.PlainText)
	})

	test.Run("should insert a boundary before a block wrapper inside a table cell when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<table><tr><td>prefix<div>block</div></td></tr></table>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, `<table><tr><td>prefix<br>block</td></tr></table>`, prepared.HTML)
		assert.Equal(test, "prefix\nblock", prepared.PlainText)
	})

	test.Run("should retain ordered list markers when flattening a table cell when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<table><tr><td><ol><li>First</li><li>Second</li></ol></td></tr></table>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, `<table><tr><td>1. First<br>2. Second</td></tr></table>`, prepared.HTML)
	})

	test.Run("should not add an empty leading line before a paragraph in a cell when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<table><tr><td><p>First</p></td><td>prefix<p>Second</p></td></tr></table>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, `<table><tr><td>First</td><td>prefix<br>Second</td></tr></table>`, prepared.HTML)
	})

	test.Run("should not add an empty leading line before a list in a cell when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<table><tr><td><ul><li>First</li><li>Second</li></ul></td><td>prefix<ul><li>Next</li></ul></td></tr></table>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, `<table><tr><td>• First<br>• Second</td><td>prefix<br>• Next</td></tr></table>`, prepared.HTML)
	})

	test.Run("should retain supported ordered numbering in plain fallback when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<div><ol start="5"><li>Fifth</li><li value="9">Ninth</li><li>Next</li></ol></div>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "5. Fifth\n9. Ninth\n10. Next", prepared.PlainText)
	})

	test.Run("should retain reversed ordered numbering in plain fallback when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `<ol reversed><li>Third</li><li>Second</li><li>First</li></ol>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "3. Third\n2. Second\n1. First", prepared.PlainText)
	})

	test.Run("should separate inline text from a list in plain fallback when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `Intro<ol start="5"><li>First</li></ol>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "Intro\n5. First", prepared.PlainText)
	})

	test.Run("should separate inline text from a table in plain fallback when adapting generic HTML", func(test *testing.T) {
		// Given
		fragment := `Intro<table><tr><td>one</td><td>two</td></tr><tr><td>three</td></tr></table>`

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "Intro\none | two\nthree", prepared.PlainText)
	})

}

func TestPrepareRichMessageInputLimit(test *testing.T) {
	test.Run("should reject a single text node above the input byte limit before parsing HTML", func(test *testing.T) {
		// Given
		fragment := strings.Repeat("x", maxHTMLBytes+1)

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.True(test, err != nil, "oversized HTML input should be rejected")
		assert.Equal(test, preparedRichMessage{}, prepared)
	})

	test.Run("should reject a many-node fragment beyond the tree node limit", func(test *testing.T) {
		// Given
		fragment := strings.Repeat("<b>x</b>", maxHTMLNodes/2+1)

		// When
		prepared, err := prepareRichMessage(fragment)

		// Then
		assert.True(test, err != nil, "excessive HTML node count should be rejected")
		assert.Equal(test, preparedRichMessage{}, prepared)
	})
}

func TestPrepareRichMessageLimits(test *testing.T) {
	test.Run("should keep rich text at the documented limit and fall back above it when preparing HTML", func(test *testing.T) {
		// Given
		withinLimit := strings.Repeat("x", richTextLimit)
		aboveLimit := strings.Repeat("x", richTextLimit+1)

		// When
		within, withinErr := prepareRichMessage(withinLimit)
		above, aboveErr := prepareRichMessage(aboveLimit)

		// Then
		assert.NoError(test, withinErr)
		assert.NoError(test, aboveErr)
		assert.True(test, within.HTML != "", "text at the limit should retain rich delivery")
		assert.Equal(test, "", above.HTML)
		assert.Equal(test, aboveLimit, above.PlainText)
	})

	test.Run("should keep rich blocks at the documented limit and fall back above it when preparing HTML", func(test *testing.T) {
		// Given
		withinLimit := strings.Repeat("<p>x</p>", richBlockLimit)
		aboveLimit := strings.Repeat("<p>x</p>", richBlockLimit+1)

		// When
		within, withinErr := prepareRichMessage(withinLimit)
		above, aboveErr := prepareRichMessage(aboveLimit)

		// Then
		assert.NoError(test, withinErr)
		assert.NoError(test, aboveErr)
		assert.True(test, within.HTML != "", "blocks at the limit should retain rich delivery")
		assert.Equal(test, "", above.HTML)
		assert.Equal(test, richBlockLimit+1, strings.Count(above.PlainText, "x"))
	})

	test.Run("should keep formatting depth at the documented limit and fall back above it when preparing HTML", func(test *testing.T) {
		// Given
		withinLimit := strings.Repeat("<strong>", richDepthLimit) + "x" + strings.Repeat("</strong>", richDepthLimit)
		aboveLimit := strings.Repeat("<strong>", richDepthLimit+1) + "x" + strings.Repeat("</strong>", richDepthLimit+1)

		// When
		within, withinErr := prepareRichMessage(withinLimit)
		above, aboveErr := prepareRichMessage(aboveLimit)

		// Then
		assert.NoError(test, withinErr)
		assert.NoError(test, aboveErr)
		assert.True(test, within.HTML != "", "formatting at the limit should retain rich delivery")
		assert.Equal(test, "", above.HTML)
		assert.Equal(test, "x", above.PlainText)
	})

	test.Run("should keep table columns at the documented limit and fall back above it when preparing HTML", func(test *testing.T) {
		// Given
		makeTable := func(columns int) string {
			cells := strings.Repeat("<td>x</td>", columns)
			return "<table><tr>" + cells + "</tr></table>"
		}
		withinLimit := makeTable(richColumnLimit)
		aboveLimit := makeTable(richColumnLimit + 1)

		// When
		within, withinErr := prepareRichMessage(withinLimit)
		above, aboveErr := prepareRichMessage(aboveLimit)

		// Then
		assert.NoError(test, withinErr)
		assert.NoError(test, aboveErr)
		assert.True(test, within.HTML != "", "table at the column limit should retain rich delivery")
		assert.Equal(test, "", above.HTML)
		assert.Equal(test, 21, strings.Count(above.PlainText, "x"))
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

	test.Run("should stop chunked fallback when context is canceled after the first chunk", func(test *testing.T) {
		// Given
		environment := setup(test)
		ctx, cancel := context.WithCancel(test.Context())
		environment.plainCancel = cancel
		fragment := `<table><tr><td>` + strings.Repeat("x", 5000) + `</td><td>y</td></tr><tr><td>z</td></tr></table>`

		// When
		err := environment.bot.sendRich(ctx, 101, fragment)

		// Then
		assert.ErrorIs(test, err, context.Canceled)
		assert.Equal(test, 1, environment.plainCalls, "cancellation should stop before sending another chunk")
		assert.Equal(test, 1, len(environment.replies), "the first successful chunk should be delivered once")
	})

	test.Run("should stop chunked fallback after the first failed chunk without retrying earlier chunks", func(test *testing.T) {
		// Given
		environment := setup(test)
		environment.plainFailOnCall = 2
		fragment := `<table><tr><td>` + strings.Repeat("x", 5000) + `</td><td>y</td></tr><tr><td>z</td></tr></table>`

		// When
		err := environment.bot.sendRich(test.Context(), 101, fragment)

		// Then
		assert.True(test, err != nil, "failed chunk should stop the delivery")
		assert.Equal(test, 2, environment.plainCalls, "delivery should stop at the first failed chunk")
		assert.Equal(test, 1, len(environment.replies), "the earlier successful chunk should not be resent")
	})
}
