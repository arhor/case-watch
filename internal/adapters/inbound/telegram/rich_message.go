package telegram

import (
	"context"
	"errors"
	stdhtml "html"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	telegramApi "github.com/go-telegram/bot"
	telegramModels "github.com/go-telegram/bot/models"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/arhor/case-watch/internal/services"
)

const (
	richTextLimit   = 32768
	plainTextLimit  = 4096
	richBlockLimit  = 500
	richDepthLimit  = 16
	richColumnLimit = 20
)

type preparedRichMessage struct {
	HTML      string
	PlainText string
}

func (this *Bot) sendRich(ctx context.Context, id int64, fragment string) error {
	prepared, err := prepareRichMessage(fragment)
	if err != nil {
		return err
	}
	this.replyMu.Lock()
	defer this.replyMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.repliesFailed {
		return ErrInvalidToken
	}
	if id <= 0 {
		return services.ErrDenied
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if prepared.HTML != "" {
		if err := bounded.Err(); err != nil {
			return err
		}
		_, err = this.client.SendRichMessage(bounded, &telegramApi.SendRichMessageParams{
			ChatID:      id,
			RichMessage: telegramModels.InputRichMessage{HTML: prepared.HTML, SkipEntityDetection: true},
		})
		if this.tokenRejected(err) {
			this.repliesFailed = true
			return ErrInvalidToken
		}
		if err != nil {
			return errors.New("telegram rich message delivery failed")
		}
		return nil
	}
	return this.sendPlainChunks(bounded, id, prepared.PlainText)
}

func (this *Bot) replyRich(ctx context.Context, id int64, fragment string) {
	if err := this.sendRich(ctx, id, fragment); err != nil {
		log.Print("telegram reply failure")
	}
}

func prepareRichMessage(fragment string) (preparedRichMessage, error) {
	if len(fragment) > maxHTMLBytes {
		return preparedRichMessage{}, errors.New("HTML fragment exceeds safe processing limits")
	}
	fragment = strings.ToValidUTF8(fragment, "\uFFFD")
	contextNode := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), contextNode)
	if err != nil {
		return preparedRichMessage{}, errors.New("HTML fragment could not be parsed safely")
	}
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, node := range nodes {
		root.AppendChild(node)
	}
	if !sanitizeTree(root, 0) {
		return preparedRichMessage{}, errors.New("HTML fragment exceeds safe processing limits")
	}
	var rich strings.Builder
	state := &renderState{}
	if err := renderRichChildren(&rich, root, state, 0, false); err != nil {
		rich.Reset()
	}
	plain := renderPlain(root)
	if plain == "" {
		return preparedRichMessage{}, errors.New("HTML fragment has no safe readable content")
	}
	if unicodeCount(plain) > richTextLimit {
		rich.Reset()
	}
	if state.blocks > richBlockLimit || state.depth > richDepthLimit {
		rich.Reset()
	}
	if !validListStructure(root) {
		rich.Reset()
	}
	if rich.Len() > 0 {
		return preparedRichMessage{HTML: rich.String(), PlainText: plain}, nil
	}
	return preparedRichMessage{PlainText: plain}, nil
}

// -- Internals ----------------------------------------------------------------

const (
	maxHTMLBytes = 1 << 20 // Bound parser memory and work before building the HTML tree.
	maxHTMLNodes = 50000
)

type renderState struct {
	blocks         int
	depth          int
	cellHasContent bool
}

func sanitizeTree(node *html.Node, depth int) bool {
	if depth > 256 {
		return false
	}
	count := 0
	var visit func(*html.Node, int) bool
	visit = func(current *html.Node, level int) bool {
		count++
		if count > maxHTMLNodes || level > 256 {
			return false
		}
		for child := current.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == html.ElementNode && (excludedTag(child.Data) || hiddenNode(child)) {
				current.RemoveChild(child)
			} else if !visit(child, level+1) {
				return false
			}
			child = next
		}
		return true
	}
	return visit(node, depth)
}

func excludedTag(tag string) bool {
	switch strings.ToLower(tag) {
	case "head", "title", "meta", "base", "link", "script", "style", "template", "iframe", "object", "embed", "form", "input", "textarea", "select", "option", "button", "audio", "video", "source", "img", "svg", "canvas", "tg-emoji", "tg-spoiler", "tg-user", "tg-mention", "tg-bot-command", "tg-quote", "tg-button", "tg-button-row", "tg-map", "tg-document", "tg-collage", "tg-slideshow", "tg-math", "tg-math-block", "tg-reference", "tg-time":
		return true
	default:
		return false
	}
}

func hiddenNode(node *html.Node) bool {
	for _, attribute := range node.Attr {
		key, value := strings.ToLower(attribute.Key), strings.ToLower(strings.TrimSpace(attribute.Val))
		if key == "hidden" || key == "aria-hidden" && value == "true" {
			return true
		}
		if key == "style" {
			for _, declaration := range strings.Split(value, ";") {
				parts := strings.SplitN(declaration, ":", 2)
				if len(parts) != 2 {
					continue
				}
				property, value := strings.TrimSpace(parts[0]), strings.TrimSpace(strings.Split(parts[1], "!")[0])
				if property == "display" && value == "none" || property == "visibility" && (value == "hidden" || value == "collapse") {
					return true
				}
			}
		}
	}
	return false
}

func renderRichChildren(output *strings.Builder, node *html.Node, state *renderState, depth int, inCell bool) error {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if isBlockWrapper(child) {
			needsSeparator := inCell && state.cellHasContent && !hasTrailingCellBreak(child.PrevSibling, child)
			needsSeparator = needsSeparator || !inCell && hasPrecedingInlineContent(child)
			if needsSeparator {
				output.WriteString("<br>")
			}
		}
		if err := renderRichNode(output, child, state, depth, inCell); err != nil {
			return err
		}
		if child.NextSibling != nil && isBlockWrapper(child) && !hasRichBlockBoundary(child.NextSibling) {
			output.WriteString("<br>")
		}
	}
	return nil
}

func hasTrailingCellBreak(previous, next *html.Node) bool {
	if previous == nil || previous.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(previous.Data) {
	case "hr", "br":
		return true
	case "div", "section", "article":
		return isBlockWrapper(next) && !hasRichBlockBoundary(next)
	default:
		return false
	}
}

func hasRichBlockBoundary(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(node.Data) {
	case "p", "h1", "h2", "h3", "h4", "h5", "h6", "pre", "ul", "ol", "li", "table", "tr", "hr", "br":
		return true
	default:
		return false
	}
}

func isBlockWrapper(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(node.Data) {
	case "div", "section", "article":
		return true
	default:
		return false
	}
}

func hasPrecedingInlineContent(node *html.Node) bool {
	for current := node; current != nil; {
		for previous := current.PrevSibling; previous != nil; previous = previous.PrevSibling {
			if previous.Type == html.CommentNode || previous.Type == html.DoctypeNode {
				continue
			}
			if previous.Type == html.TextNode && strings.TrimSpace(previous.Data) == "" {
				continue
			}
			if hasRichBlockBoundary(previous) || isBlockWrapper(previous) {
				return false
			}
			return hasVisibleText(previous)
		}
		parent := current.Parent
		if parent == nil || parent.Type != html.ElementNode || parent.Data != "span" && parent.Data != "label" {
			return false
		}
		current = parent
	}
	return false
}

func hasVisibleText(node *html.Node) bool {
	if node.Type == html.TextNode {
		return strings.TrimSpace(node.Data) != ""
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if hasVisibleText(child) {
			return true
		}
	}
	return false
}

func renderRichNode(output *strings.Builder, node *html.Node, state *renderState, depth int, inCell bool) error {
	if depth > richDepthLimit {
		return errors.New("rich nesting exceeds limit")
	}
	switch node.Type {
	case html.TextNode:
		output.WriteString(stdhtml.EscapeString(node.Data))
		if inCell && strings.TrimSpace(node.Data) != "" {
			state.cellHasContent = true
		}
		return nil
	case html.CommentNode, html.DoctypeNode:
		return nil
	case html.ElementNode:
	}
	tag := strings.ToLower(node.Data)
	if tag == "thead" || tag == "tbody" || tag == "tfoot" || tag == "div" || tag == "section" || tag == "article" || tag == "span" || tag == "label" {
		return renderRichChildren(output, node, state, depth, inCell)
	}
	if tag == "table" {
		if inCell || !validTable(node) {
			return errors.New("unsupported table structure")
		}
		state.blocks++
		output.WriteString("<table>")
		for row := node.FirstChild; row != nil; row = row.NextSibling {
			if err := renderRichTableRows(output, row, state, depth+1); err != nil {
				return err
			}
		}
		output.WriteString("</table>")
		return nil
	}
	if tag == "tr" || tag == "td" || tag == "th" {
		return errors.New("table element outside table")
	}
	if tag == "hr" {
		if inCell {
			if state.cellHasContent && !hasTrailingCellBreak(node.PrevSibling, node) {
				output.WriteString("<br>")
			}
			output.WriteString("────<br>")
			state.cellHasContent = true
		} else {
			output.WriteString("<hr>")
		}
		state.blocks++
		return nil
	}
	if tag == "br" {
		output.WriteString("<br>")
		return nil
	}
	if inCell && (tag == "ul" || tag == "ol") {
		return renderRichChildren(output, node, state, depth, true)
	}
	if inCell && tag == "li" {
		if state.cellHasContent && !hasTrailingCellBreak(node.PrevSibling, node) {
			output.WriteString("<br>")
		}
		output.WriteString(listItemMarker(node))
		return renderRichChildren(output, node, state, depth+1, true)
	}
	if inCell && (tag == "p" || tag == "pre" || strings.HasPrefix(tag, "h")) {
		if state.cellHasContent && !hasTrailingCellBreak(node.PrevSibling, node) {
			output.WriteString("<br>")
		}
		return renderRichChildren(output, node, state, depth, true)
	}
	if tag == "a" {
		href := safeHref(node)
		if href != "" {
			output.WriteString(`<a href="`)
			output.WriteString(stdhtml.EscapeString(href))
			output.WriteString(`">`)
		}
		if err := renderRichChildren(output, node, state, depth+1, inCell); err != nil {
			return err
		}
		if href != "" {
			output.WriteString("</a>")
		}
		return nil
	}
	allowed := map[string]bool{
		"p":      true,
		"h1":     true,
		"h2":     true,
		"h3":     true,
		"h4":     true,
		"h5":     true,
		"h6":     true,
		"pre":    true,
		"code":   true,
		"ul":     true,
		"ol":     true,
		"li":     true,
		"strong": true,
		"b":      true,
		"em":     true,
		"i":      true,
		"u":      true,
		"ins":    true,
		"s":      true,
		"del":    true,
		"strike": true,
	}
	if !allowed[tag] {
		return renderRichChildren(output, node, state, depth, inCell)
	}
	state.blocks++
	output.WriteString("<")
	output.WriteString(tag)
	output.WriteString(">")
	if err := renderRichChildren(output, node, state, depth+1, inCell); err != nil {
		return err
	}
	output.WriteString("</")
	output.WriteString(tag)
	output.WriteString(">")
	return nil
}

func renderRichTableRows(output *strings.Builder, node *html.Node, state *renderState, depth int) error {
	if node.Type == html.ElementNode && node.Data == "tr" {
		state.blocks++
		output.WriteString("<tr>")
		for cell := node.FirstChild; cell != nil; cell = cell.NextSibling {
			if cell.Type != html.ElementNode || cell.Data != "td" && cell.Data != "th" {
				continue
			}
			state.blocks++
			output.WriteString("<")
			output.WriteString(cell.Data)
			output.WriteString(">")
			previousCellHasContent := state.cellHasContent
			state.cellHasContent = false
			if err := renderRichChildren(output, cell, state, depth+1, true); err != nil {
				state.cellHasContent = previousCellHasContent
				return err
			}
			state.cellHasContent = previousCellHasContent
			output.WriteString("</")
			output.WriteString(cell.Data)
			output.WriteString(">")
		}
		output.WriteString("</tr>")
		return nil
	}
	if node.Type == html.ElementNode && node.Data == "table" {
		return errors.New("nested table")
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if err := renderRichTableRows(output, child, state, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validTable(table *html.Node) bool {
	width := -1
	var valid bool = true
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node != table && node.Type == html.ElementNode && node.Data == "table" {
			valid = false
			return
		}
		if node != table && node.Type == html.ElementNode {
			insideCell := false
			for parent := node.Parent; parent != nil && parent != table; parent = parent.Parent {
				if parent.Type == html.ElementNode && (parent.Data == "td" || parent.Data == "th") {
					insideCell = true
					break
				}
			}
			if !insideCell {
				switch node.Data {
				case "thead", "tbody", "tfoot", "tr", "td", "th":
				default:
					valid = false
				}
			}
		}
		if node.Type == html.ElementNode && node.Data == "tr" {
			columns := 0
			for cell := node.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Type != html.ElementNode || cell.Data != "td" && cell.Data != "th" {
					continue
				}
				columns++
				for _, attribute := range cell.Attr {
					if (attribute.Key == "rowspan" || attribute.Key == "colspan") && attribute.Val != "1" {
						valid = false
					}
				}
			}
			if columns == 0 || columns > richColumnLimit || width >= 0 && width != columns {
				valid = false
			}
			width = columns
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(table)
	return valid && width > 0
}

func validListStructure(root *html.Node) bool {
	valid := true
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if node.Data == "ol" {
				for _, attribute := range node.Attr {
					if attribute.Key == "start" || attribute.Key == "type" || attribute.Key == "reversed" {
						valid = false
					}
				}
			}
			if node.Data == "li" {
				for _, attribute := range node.Attr {
					if attribute.Key == "value" || attribute.Key == "type" {
						valid = false
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return valid
}

func safeHref(node *html.Node) string {
	for _, attribute := range node.Attr {
		if attribute.Key == "href" {
			value := strings.TrimSpace(attribute.Val)
			parsed, err := url.Parse(value)
			if err != nil || strings.ContainsAny(value, "\r\n\t ") || parsed.User != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
				return ""
			}
			return value
		}
	}
	return ""
}

func renderPlain(node *html.Node) string {
	var output strings.Builder
	preformattedContents := make([]string, 0)
	markerPrefix := "\xffcase-watch-preformatted-"
	var walk func(*html.Node, bool)
	walk = func(current *html.Node, inCell bool) {
		if current.Type == html.TextNode {
			output.WriteString(current.Data)
			return
		}
		if current.Type != html.ElementNode {
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child, inCell)
			}
			return
		}
		tag := strings.ToLower(current.Data)
		if isPlainBlock(current) && tag != "br" && hasPrecedingInlineContent(current) {
			output.WriteString("\n")
		}
		if tag == "table" {
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child, inCell)
			}
			output.WriteString("\n")
			return
		}
		if tag == "tr" {
			first := true
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode && (child.Data == "td" || child.Data == "th") {
					if !first {
						output.WriteString(" | ")
					}
					first = false
					walk(child, true)
				}
			}
			output.WriteString("\n")
			return
		}
		if tag == "pre" {
			marker := markerPrefix + strconv.Itoa(len(preformattedContents)) + "\xfe"
			preformattedContents = append(preformattedContents, plainNodeText(current))
			output.WriteString(marker)
			output.WriteString("\n")
			return
		}
		block := tag == "p" || strings.HasPrefix(tag, "h") || tag == "div" || tag == "section" || tag == "article" || tag == "li" || tag == "pre" || tag == "hr" || tag == "br" || tag == "caption"
		if tag == "li" {
			output.WriteString(listItemMarker(current))
		}
		if tag == "hr" {
			output.WriteString("────")
		} else if tag != "br" {
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				walk(child, inCell)
			}
		}
		if tag == "a" {
			href := safeHref(current)
			if href != "" {
				label := strings.TrimSpace(plainNodeText(current))
				if href != label {
					output.WriteString(" (")
					output.WriteString(href)
					output.WriteString(")")
				}
			}
		}
		if block {
			output.WriteString("\n")
		}
	}
	walk(node, false)
	plainWithMarkers := strings.TrimSpace(output.String())
	var plain strings.Builder
	plain.Grow(len(plainWithMarkers))
	for offset := 0; offset < len(plainWithMarkers); {
		markerStart := strings.Index(plainWithMarkers[offset:], markerPrefix)
		if markerStart < 0 {
			plain.WriteString(plainWithMarkers[offset:])
			break
		}
		markerStart += offset
		plain.WriteString(plainWithMarkers[offset:markerStart])
		markerEndOffset := strings.IndexByte(plainWithMarkers[markerStart:], '\xfe')
		if markerEndOffset < 0 {
			plain.WriteString(plainWithMarkers[markerStart:])
			break
		}
		markerEnd := markerStart + markerEndOffset
		preformattedIndex, err := strconv.Atoi(plainWithMarkers[markerStart+len(markerPrefix) : markerEnd])
		if err != nil || preformattedIndex < 0 || preformattedIndex >= len(preformattedContents) {
			plain.WriteString(plainWithMarkers[markerStart : markerStart+len(markerPrefix)])
			offset = markerStart + len(markerPrefix)
			continue
		}
		plain.WriteString(preformattedContents[preformattedIndex])
		offset = markerEnd + 1
	}
	return plain.String()
}

func isPlainBlock(node *html.Node) bool {
	if node.Type != html.ElementNode {
		return false
	}
	tag := strings.ToLower(node.Data)
	return isBlockWrapper(node) || tag == "p" || strings.HasPrefix(tag, "h") || tag == "pre" || tag == "li" || tag == "hr" || tag == "table" || tag == "ul" || tag == "ol"
}

func listItemMarker(node *html.Node) string {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type != html.ElementNode || parent.Data != "ul" && parent.Data != "ol" {
			continue
		}
		if parent.Data == "ul" {
			return "• "
		}
		return orderedListItemMarker(parent, node)
	}
	return "• "
}

func orderedListItemMarker(list, item *html.Node) string {
	if hasAttribute(list, "type") || hasAttribute(item, "type") {
		return "• "
	}
	if hasAttribute(list, "start") {
		if _, ok := integerAttribute(list, "start"); !ok {
			return "• "
		}
	}
	for sibling := list.FirstChild; sibling != nil; sibling = sibling.NextSibling {
		if sibling.Type == html.ElementNode && sibling.Data == "li" && hasAttribute(sibling, "value") {
			if _, ok := integerAttribute(sibling, "value"); !ok {
				return "• "
			}
		}
	}
	reversed := hasAttribute(list, "reversed")
	step, number := 1, 1
	if reversed {
		step = -1
		number = directListItemCount(list)
	}
	if start, ok := integerAttribute(list, "start"); ok {
		number = start
	}
	for sibling := list.FirstChild; sibling != nil; sibling = sibling.NextSibling {
		if sibling.Type != html.ElementNode || sibling.Data != "li" {
			continue
		}
		if explicit, ok := integerAttribute(sibling, "value"); ok {
			number = explicit
		}
		if sibling == item {
			return strconv.Itoa(number) + ". "
		}
		number += step
	}
	return "• "
}

func directListItemCount(list *html.Node) int {
	count := 0
	for child := list.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "li" {
			count++
		}
	}
	return count
}

func listAttribute(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func hasAttribute(node *html.Node, name string) bool {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return true
		}
	}
	return false
}

func integerAttribute(node *html.Node, name string) (int, bool) {
	value := listAttribute(node, name)
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil
}

func plainNodeText(node *html.Node) string {
	var result strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			result.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	return result.String()
}

func unicodeCount(value string) int { return len([]rune(value)) }

func splitPlainText(value string) []string {
	var chunks []string
	for len(value) > 0 {
		if unicodeCount(value) <= plainTextLimit {
			chunks = append(chunks, value)
			break
		}
		end := plainTextLimit
		for end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		if boundary := strings.LastIndexAny(value[:end], " \n\t"); boundary > 0 {
			end = boundary + 1
		}
		chunks = append(chunks, value[:end])
		value = value[end:]
	}
	return chunks
}

func (this *Bot) sendPlainChunks(ctx context.Context, id int64, text string) error {
	for _, chunk := range splitPlainText(text) {
		if err := ctx.Err(); err != nil {
			return err
		}
		linkPreviewDisabled := true
		_, err := this.client.SendMessage(ctx, &telegramApi.SendMessageParams{ChatID: id, Text: chunk, LinkPreviewOptions: &telegramModels.LinkPreviewOptions{IsDisabled: &linkPreviewDisabled}})
		if this.tokenRejected(err) {
			this.repliesFailed = true
			return ErrInvalidToken
		}
		if err != nil {
			return errors.New("telegram message delivery failed")
		}
	}
	return nil
}
