package vaadin

import (
	"errors"
	"html"
	"strings"
)

// RenderHTML renders ordered, non-overlapping roots from tree as one static HTML
// fragment. Zero/stale/foreign handles and malformed selected structures return
// an error with no partial fragment. Empty selections/content return "", nil.
// The caller owns ordering and selection, including excluded ancestors. Neither
// the tree nor its handles are modified. See HTML.md for the bounded policy.
func RenderHTML(tree *StateTree, roots ...Node) (string, error) {
	if len(roots) == 0 {
		return "", nil
	}
	if tree == nil {
		return "", errors.New("missing render tree")
	}
	// Validate even excluded subtrees before producing any output. Identity checks
	// reject handles from other trees or previous registrations with reused IDs.
	seen := map[NodeID]bool{}
	type visit struct {
		node  Node
		depth int
	}
	pending := []visit{}
	for _, root := range roots {
		pending = append(pending, visit{root, 0})
	}
	for len(pending) > 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		registered, exists := tree.FindByID(current.node.ID())
		if current.node.state == nil || !exists || registered != current.node || seen[current.node.ID()] || current.depth > 512 {
			return "", errors.New("invalid render selection or structure")
		}
		seen[current.node.ID()] = true
		for _, properties := range current.node.state.features {
			for _, value := range properties {
				if reference, ok := value.(NodeReference); ok {
					if _, exists := tree.FindByID(reference.ID); !exists {
						return "", errors.New("unresolved render property")
					}
				}
			}
		}
		for _, childID := range current.node.ChildIDs() {
			child, exists := tree.FindByID(childID)
			if !exists {
				return "", errors.New("unresolved render child")
			}
			pending = append(pending, visit{child, current.depth + 1})
		}
	}
	renderer := htmlRenderer{tree: tree}
	return renderer.container(roots, renderContext{}), nil
}

// -- Internals ----------------------------------------------------------------

// Logical emphasis is inherited across blocks; applied emphasis tracks markup
// already open around the current inline run.
type renderContext struct {
	bold, italic               bool
	appliedBold, appliedItalic bool
	anchor, list, caption      bool
	transparent                Node
}

type htmlRenderer struct{ tree *StateTree }

type htmlRow struct{ source, caption, value Node }

func (this htmlRenderer) children(node Node) []Node { return this.tree.Children(node.ID()) }

func (this htmlRenderer) container(nodes []Node, context renderContext) string {
	var output strings.Builder
	rows := []htmlRow{}
	flush := func() {
		if len(rows) == 0 {
			return
		}
		output.WriteString("<table><tbody>")
		for _, row := range rows {
			output.WriteString("<tr><td>")
			rowBold, rowItalic := emphasis(row.source)
			rowContext := context
			rowContext.bold = context.bold || rowBold
			rowContext.italic = context.italic || rowItalic
			rowContext.appliedBold = false
			rowContext.appliedItalic = false
			captionContext := rowContext
			captionContext.bold = true
			captionContext.appliedBold = true
			captionContext.list = false
			captionContext.caption = true
			caption := this.node(row.caption, captionContext)
			if caption != "" {
				output.WriteString("<strong>" + caption + "</strong>")
			}
			output.WriteString("</td><td>")
			valueContext := rowContext
			valueContext.list = false
			valueContext.transparent = row.value
			output.WriteString(this.node(row.value, valueContext))
			output.WriteString("</td></tr>")
		}
		output.WriteString("</tbody></table>")
		rows = nil
	}
	// Only discard whitespace in a run that actually has a row on either side.
	// Inline whitespace and ordinary empty/interactive boundaries remain in place.
	for index := 0; index < len(nodes); index++ {
		node := nodes[index]
		if IsHidden(node) {
			continue
		}
		if grouped, ok := this.rowGroup(node); ok {
			if len(grouped) > 0 {
				rows = append(rows, grouped...)
			}
			continue
		}
		if whitespaceNode(node) {
			next := index + 1
			for next < len(nodes) && (IsHidden(nodes[next]) || whitespaceNode(nodes[next])) {
				next++
			}
			if len(rows) > 0 {
				continue
			}
			if next < len(nodes) {
				if grouped, ok := this.rowGroup(nodes[next]); ok && len(grouped) > 0 {
					continue
				}
			}
		}
		flush()
		output.WriteString(this.node(node, context))
	}
	flush()
	return output.String()
}

func (this htmlRenderer) node(node Node, context renderContext) string {
	if IsHidden(node) || IsExcludedControl(node) {
		return ""
	}
	bold, italic := emphasis(node)
	desired := context
	desired.bold = context.bold || bold
	desired.italic = context.italic || italic
	tag := htmlTag(node)
	content := ""
	blockChildren := false
	if value, ok := ReadOnlyValue(node); ok {
		content = html.EscapeString(value)
		if tag == "vaadin-text-area" {
			content = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n"), "\n", "<br>")
		}
	} else {
		children := this.children(node)
		for _, child := range children {
			blockChildren = blockChildren || this.hasBlock(child)
		}
		if context.caption {
			blockChildren = false
		}
		childContext := desired
		if !blockChildren {
			childContext.appliedBold = desired.bold
			childContext.appliedItalic = desired.italic
		} else {
			childContext.appliedBold = false
			childContext.appliedItalic = false
		}
		childContext.list = (tag == "ul" || tag == "ol") && this.validList(node)
		if tag == "a" && !context.anchor && !blockChildren && safeHref(node) != "" {
			childContext.anchor = true
		}
		if text, present := node.Text(); present {
			content = html.EscapeString(text)
		}
		// Emphasis crosses blocks semantically but is emitted on inline runs only.
		if blockChildren && content != "" {
			content = wrapEmphasis(content, desired, context)
		}
		content += this.container(children, childContext)
	}
	if content == "" && tag != "br" && tag != "hr" {
		return ""
	}
	if !blockChildren {
		content = wrapEmphasis(content, desired, context)
	}
	switch tag {
	case "br", "hr":
		return "<" + tag + ">"
	case "div", "section", "article", "vaadin-vertical-layout", "vaadin-horizontal-layout":
		// Row-only groups were handled by container before reaching this fallback.
		if context.caption || context.transparent == node {
			return content
		}
		return "<div>" + content + "</div>"
	case "p", "h1", "h2", "h3", "h4", "h5", "h6", "pre", "code":
		if !blockChildren {
			return "<" + tag + ">" + content + "</" + tag + ">"
		}
	case "ul", "ol":
		if this.validList(node) {
			return "<" + tag + ">" + content + "</" + tag + ">"
		}
	case "li":
		if context.list {
			return "<li>" + content + "</li>"
		}
		return "<div>" + content + "</div>"
	case "a":
		if href := safeHref(node); href != "" && !context.anchor && !blockChildren {
			return "<a href=\"" + html.EscapeString(href) + "\">" + content + "</a>"
		}
	}
	return content
}

func wrapEmphasis(content string, desired, applied renderContext) string {
	if content == "" {
		return ""
	}
	if desired.italic && !applied.appliedItalic {
		content = "<em>" + content + "</em>"
	}
	if desired.bold && !applied.appliedBold {
		content = "<strong>" + content + "</strong>"
	}
	return content
}

func (this htmlRenderer) hasBlock(node Node) bool {
	if IsHidden(node) || IsExcludedControl(node) {
		return false
	}
	switch htmlTag(node) {
	case "div", "section", "article", "vaadin-vertical-layout", "vaadin-horizontal-layout", "p", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "pre", "hr":
		return true
	}
	for _, child := range this.children(node) {
		if this.hasBlock(child) {
			return true
		}
	}
	return false
}

func (this htmlRenderer) validList(node Node) bool {
	for _, child := range this.children(node) {
		if !IsHidden(child) && !IsExcludedControl(child) && !whitespaceNode(child) && htmlTag(child) != "li" {
			return false
		}
	}
	text, _ := node.Text()
	return strings.TrimSpace(text) == ""
}

func whitespaceNode(node Node) bool {
	text, present := node.Text()
	return present && htmlTag(node) == "" && len(node.ChildIDs()) == 0 && strings.TrimSpace(text) == ""
}
