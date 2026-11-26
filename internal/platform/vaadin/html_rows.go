package vaadin

import "strings"

func (this htmlRenderer) row(node Node) (htmlRow, bool) {
	if IsHidden(node) || IsExcludedControl(node) || htmlTag(node) != "div" || !hasClass(node, "flex-md-row") {
		return htmlRow{}, false
	}
	if text, _ := node.Text(); strings.TrimSpace(text) != "" {
		return htmlRow{}, false
	}
	columns := []Node{}
	for _, child := range this.children(node) {
		if IsHidden(child) || whitespaceNode(child) {
			continue
		}
		columns = append(columns, child)
	}
	if len(columns) != 2 || htmlTag(columns[0]) != "div" || htmlTag(columns[1]) != "div" {
		return htmlRow{}, false
	}
	left, right := columns[0], columns[1]
	paired := hasClass(left, "col-md-4") && hasClass(right, "col-md-8") || hasClass(left, "col-lg-3") && hasClass(right, "col-lg-9")
	if !paired || this.columnUnsafe(left) || this.columnUnsafe(right) {
		return htmlRow{}, false
	}
	labels, valid := this.captionShape(left, false)
	if !valid || labels != 1 || !this.hasValue(right) {
		return htmlRow{}, false
	}
	return htmlRow{node, left, right}, true
}

func (this htmlRenderer) columnUnsafe(node Node) bool {
	if IsHidden(node) {
		return false
	}
	if IsExcludedControl(node) || htmlTag(node) == "div" && hasClass(node, "flex-md-row") {
		return true
	}
	for _, child := range this.children(node) {
		if this.columnUnsafe(child) {
			return true
		}
	}
	return false
}

func (this htmlRenderer) captionShape(node Node, insideLabel bool) (int, bool) {
	if IsHidden(node) {
		return 0, true
	}
	labels := 0
	if htmlTag(node) == "label" {
		labels = 1
		insideLabel = true
	}
	if !insideLabel {
		switch htmlTag(node) {
		case "div", "vaadin-vertical-layout", "span", "strong", "b", "em", "i", "":
		default:
			return 0, false
		}
		if text, _ := node.Text(); strings.TrimSpace(text) != "" {
			return 0, false
		}
	} else {
		switch htmlTag(node) {
		case "label", "span", "strong", "b", "em", "i", "code", "br", "":
		default:
			return 0, false
		}
	}
	for _, child := range this.children(node) {
		count, valid := this.captionShape(child, insideLabel)
		if !valid {
			return 0, false
		}
		labels += count
	}
	return labels, true
}

func (this htmlRenderer) hasValue(node Node) bool {
	if IsHidden(node) || IsExcludedControl(node) {
		return false
	}
	if _, ok := ReadOnlyValue(node); ok {
		return true
	}
	for _, child := range this.children(node) {
		if this.hasValue(child) {
			return true
		}
	}
	return false
}

// rowGroup flattens only row-only neutral wrappers. An empty readonly value is
// meaningful, unlike an empty passive div. Interactive/active boundaries stop
// grouping even though rendering will subsequently omit their contents.
func (this htmlRenderer) rowGroup(node Node) ([]htmlRow, bool) {
	if IsHidden(node) {
		return nil, true
	}
	if row, ok := this.row(node); ok {
		return []htmlRow{row}, true
	}
	if htmlTag(node) != "div" && htmlTag(node) != "vaadin-vertical-layout" {
		return nil, false
	}
	if IsExcludedControl(node) {
		return nil, false
	}
	bold, italic := emphasis(node)
	text, _ := node.Text()
	if bold || italic || strings.TrimSpace(text) != "" {
		return nil, false
	}
	rows := []htmlRow{}
	for _, child := range this.children(node) {
		if whitespaceNode(child) {
			continue
		}
		nested, ok := this.rowGroup(child)
		if !ok {
			return nil, false
		}
		rows = append(rows, nested...)
	}
	return rows, true
}

func hasClass(node Node, expected string) bool {
	for _, class := range node.Classes() {
		if class == expected {
			return true
		}
	}
	return false
}
