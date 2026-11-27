package vaadin_test

import ()

func informationRow(caption string, values ...htmlFixtureNode) htmlFixtureNode {
	return rowColumns(column("col-md-4", element("label", text(caption))), column("col-md-8", values...))
}
func column(class string, children ...htmlFixtureNode) htmlFixtureNode {
	return htmlFixtureNode{tag: "div", classes: []string{class}, children: children}
}
func rowColumns(children ...htmlFixtureNode) htmlFixtureNode {
	return htmlFixtureNode{tag: "div", classes: []string{"flex-md-row"}, children: children}
}
func tableHTML(rows string) string { return "<table><tbody>" + rows + "</tbody></table>" }
