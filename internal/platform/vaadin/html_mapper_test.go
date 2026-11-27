package vaadin_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arhor/case-watch/internal/platform/vaadin"
	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestRenderHTML(test *testing.T) {
	test.Run("should preserve whitespace between inline text and a row", func(test *testing.T) {
		// Given
		tree, roots := htmlTree(test, text("intro"), text(" "), informationRow("caption", field("vaadin-text-field", "value")))

		// When
		actual, err := vaadin.RenderHTML(tree, roots...)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "intro "+tableHTML("<tr><td><strong>caption</strong></td><td>value</td></tr>"), actual)
	})
	for _, flow := range []struct {
		name, bootstrap string
		ids             []vaadin.NodeID
	}{
		{name: "authenticated", bootstrap: "bootstrap-authenticated.html", ids: []vaadin.NodeID{29, 35, 41, 53, 59, 60, 61, 70}},
		{name: "rpc", bootstrap: "bootstrap-login.html", ids: []vaadin.NodeID{42, 48, 54, 66, 72, 73, 74, 83}},
	} {
		test.Run("should match independent HTML when replaying "+flow.name, func(test *testing.T) {
			// Given
			bootstrap, err := vaadin.DecodeBootstrap(fixture(test, flow.bootstrap))
			assert.NoError(test, err)
			tree := vaadin.NewStateTree()
			assert.NoError(test, tree.Apply(bootstrap.Message.Changes))
			if flow.name == "rpc" {
				var responses []string
				assert.NoError(test, json.Unmarshal(fixture(test, "rpc-authentication.json"), &responses))
				for _, response := range responses {
					messages, err := vaadin.DecodeResponse([]byte(response))
					assert.NoError(test, err)
					for _, message := range messages {
						assert.NoError(test, tree.Apply(message.Changes))
					}
				}
			}
			roots := []vaadin.Node{}
			for _, id := range flow.ids {
				node, ok := tree.FindByID(id)
				assert.True(test, ok)
				roots = append(roots, node)
			}
			before := snapshotTree(tree)
			expected := strings.TrimSpace(string(fixture(test, "expected-content.html")))

			// When
			actual, err := vaadin.RenderHTML(tree, roots...)
			repeated, repeatError := vaadin.RenderHTML(tree, roots...)
			after := snapshotTree(tree)

			// Then
			assert.NoError(test, err)
			assert.NoError(test, repeatError)
			assert.Equal(test, expected, actual)
			assert.Equal(test, actual, repeated)
			assert.Equal(test, 1, strings.Count(actual, "<table>"))
			assert.Equal(test, 7, strings.Count(actual, "<tr>"))
			assert.Equal(test, 14, strings.Count(actual, "<td>"))
			assert.DeepEqual(test, before, after)
		})
	}
	for _, scenario := range []struct {
		name     string
		nodes    []htmlFixtureNode
		expected string
	}{
		{name: "escape Unicode and hostile field values", nodes: []htmlFixtureNode{field("vaadin-text-field", "<script>Łódź & \"x\"</script>")}, expected: "&lt;script&gt;Łódź &amp; &#34;x&#34;&lt;/script&gt;"},
		{name: "preserve textarea consecutive newlines", nodes: []htmlFixtureNode{field("vaadin-text-area", "a\r\n\rβ\n")}, expected: "a<br><br>β<br>"},
		{name: "preserve inline spaces", nodes: []htmlFixtureNode{element("span", text("a"), text(" "), element("b", text("b")))}, expected: "a <strong>b</strong>"},
		{name: "avoid redundant emphasis", nodes: []htmlFixtureNode{element("b", element("strong", element("i", element("em", text("x")))))}, expected: "<strong><em>x</em></strong>"},
		{name: "retain unknown passive descendants", nodes: []htmlFixtureNode{element("vaadin-unknown", element("p", text("readable")))}, expected: "<p>readable</p>"},
		{name: "repair block inside paragraph", nodes: []htmlFixtureNode{element("p", text("before"), element("div", text("inside")), text("after"))}, expected: "before<div>inside</div>after"},
		{name: "apply emphasis inside blocks", nodes: []htmlFixtureNode{element("strong", element("div", text("inside")))}, expected: "<div><strong>inside</strong></div>"},
		{name: "repair nested anchors", nodes: []htmlFixtureNode{link("https://example.invalid", text("outer"), link("https://example.invalid/inner", text("inner")))}, expected: "<a href=\"https://example.invalid\">outerinner</a>"},
		{name: "repair orphan list item", nodes: []htmlFixtureNode{element("li", text("one"))}, expected: "<div>one</div>"},
		{name: "preserve valid list", nodes: []htmlFixtureNode{element("ul", element("li", text("one")), element("li", text("two")))}, expected: "<ul><li>one</li><li>two</li></ul>"},
		{name: "repair list with non-item children", nodes: []htmlFixtureNode{element("ul", text("before"), element("li", text("one")))}, expected: "before<div>one</div>"},
		{name: "keep safe link and escape attribute", nodes: []htmlFixtureNode{link("https://example.invalid/?a=1&b=\"x\"", text("<go>"))}, expected: "<a href=\"https://example.invalid/?a=1&amp;b=&#34;x&#34;\">&lt;go&gt;</a>"},
		{name: "map normalized semantic styles", nodes: []htmlFixtureNode{{tag: "span", styles: map[string]any{"font-weight": " 900 ", "font-style": " OBLIQUE "}, children: []htmlFixtureNode{text("x")}}}, expected: "<strong><em>x</em></strong>"},
		{name: "repair block inside link", nodes: []htmlFixtureNode{link("https://example.invalid", element("p", text("x")))}, expected: "<p>x</p>"},
		{name: "repair block inside code", nodes: []htmlFixtureNode{element("code", element("div", text("x")))}, expected: "<div>x</div>"},

		{name: "keep empty content empty", nodes: []htmlFixtureNode{element("div")}, expected: ""},
	} {
		test.Run("should "+scenario.name+" when rendering synthetic nodes", func(test *testing.T) {
			// Given
			tree, roots := htmlTree(test, scenario.nodes...)

			// When
			actual, err := vaadin.RenderHTML(tree, roots...)

			// Then
			assert.NoError(test, err)
			assert.Equal(test, scenario.expected, actual)
		})
	}
	for _, href := range []string{"javascript:alert(1)", "data:text/html,x", "file:///x", "//example.invalid", "/relative", "https://user:password@example.invalid", "https://example.invalid/with space", "https://example.invalid/\n", "https://"} {
		test.Run("should retain readable text when URL is unsafe "+href, func(test *testing.T) {
			// Given
			tree, roots := htmlTree(test, link(href, text("readable")))

			// When
			actual, err := vaadin.RenderHTML(tree, roots...)

			// Then
			assert.NoError(test, err)
			assert.Equal(test, "readable", actual)
		})
	}
	for _, tag := range []string{"SCRIPT", "INPUT", "script", "style", "template", "iframe", "svg", "math", "img", "audio", "video", "form", "input", "button", "textarea", "select", "vaadin-password-field", "vaadin-button", "vaadin-checkbox", "vaadin-radio-button", "vaadin-select", "vaadin-combo-box", "vaadin-date-picker"} {
		test.Run("should omit entire subtree when source is "+tag, func(test *testing.T) {
			// Given
			source := field(tag, "secret")
			source.children = []htmlFixtureNode{text("secret")}
			tree, roots := htmlTree(test, source)

			// When
			actual, err := vaadin.RenderHTML(tree, roots...)

			// Then
			assert.NoError(test, err)
			assert.Equal(test, "", actual)
		})
	}
	test.Run("should return empty fragment when selection is empty", func(test *testing.T) {
		// Given
		var tree *vaadin.StateTree

		// When
		actual, err := vaadin.RenderHTML(tree)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, "", actual)
	})
	test.Run("should reject invalid selections when handles overlap or are unregistered", func(test *testing.T) {
		// Given
		tree, roots := htmlTree(test, element("div", text("valid")))
		foreign, foreignRoots := htmlTree(test, text("foreign"))
		_ = foreign
		descendant := tree.Children(roots[0].ID())[0]
		before := snapshotTree(tree)

		// When
		selections := [][]vaadin.Node{{{}}, {roots[0], roots[0]}, {roots[0], descendant}, {descendant, roots[0]}, {roots[0], foreignRoots[0]}}
		for _, selection := range selections {
			actual, err := vaadin.RenderHTML(tree, selection...)
			assert.True(test, err != nil)
			assert.Equal(test, "", actual)
		}
		actual, nilError := vaadin.RenderHTML(nil, roots...)

		// Then
		assert.True(test, nilError != nil)
		assert.Equal(test, "", actual)
		assert.DeepEqual(test, before, snapshotTree(tree))
	})

	for _, scenario := range []struct {
		name     string
		nodes    []htmlFixtureNode
		expected string
	}{
		{name: "one selected row", nodes: []htmlFixtureNode{informationRow("Arbitrary caption", field("vaadin-text-field", "value"))}, expected: tableHTML("<tr><td><strong>Arbitrary caption</strong></td><td>value</td></tr>")},
		{name: "additional localized rows and nested groups", nodes: []htmlFixtureNode{informationRow("日本語", field("vaadin-text-field", "一")), element("div", element("vaadin-vertical-layout", informationRow("Dodatkowe", field("vaadin-text-field", "dwa")))), element("div"), informationRow("", field("vaadin-text-field", ""))}, expected: tableHTML("<tr><td><strong>日本語</strong></td><td>一</td></tr><tr><td><strong>Dodatkowe</strong></td><td>dwa</td></tr><tr><td></td><td></td></tr>")},
		{name: "nested values and separators", nodes: []htmlFixtureNode{informationRow("Documents", element("div", field("vaadin-text-field", "a"), element("hr"), field("vaadin-text-area", "b\r\nc")))}, expected: tableHTML("<tr><td><strong>Documents</strong></td><td><div>a<hr>b<br>c</div></td></tr>")},
		{name: "neutral parent", nodes: []htmlFixtureNode{element("div", informationRow("A", field("vaadin-text-field", "1")), informationRow("B", field("vaadin-text-field", "2")))}, expected: tableHTML("<tr><td><strong>A</strong></td><td>1</td></tr><tr><td><strong>B</strong></td><td>2</td></tr>")},
	} {
		test.Run("should group compatible rows when source contains "+scenario.name, func(test *testing.T) {
			// Given
			tree, roots := htmlTree(test, scenario.nodes...)

			// When
			actual, err := vaadin.RenderHTML(tree, roots...)

			// Then
			assert.NoError(test, err)
			assert.Equal(test, scenario.expected, actual)
		})
	}
	for _, scenario := range []struct {
		boundary htmlFixtureNode
		expected string
	}{
		{boundary: element("h2", text("Heading")), expected: "<h2>Heading</h2>"},
		{boundary: element("p", text("paragraph")), expected: "<p>paragraph</p>"},
		{boundary: element("hr"), expected: "<hr>"},
		{boundary: element("form", text("secret")), expected: ""},
		{boundary: element("div", informationRow("Inner", field("vaadin-text-field", "inside")), element("p", text("mixed"))), expected: "<div>" + tableHTML("<tr><td><strong>Inner</strong></td><td>inside</td></tr>") + "<p>mixed</p></div>"},
	} {
		test.Run("should keep row runs separate when boundary is "+scenario.boundary.tag, func(test *testing.T) {
			// Given
			tree, roots := htmlTree(test, informationRow("A", field("vaadin-text-field", "1")), scenario.boundary, informationRow("B", field("vaadin-text-field", "2")))
			first := tableHTML("<tr><td><strong>A</strong></td><td>1</td></tr>")
			last := tableHTML("<tr><td><strong>B</strong></td><td>2</td></tr>")

			// When
			actual, err := vaadin.RenderHTML(tree, roots...)

			// Then
			assert.NoError(test, err)
			assert.Equal(test, first+scenario.expected+last, actual)
		})
	}
	for _, scenario := range []struct {
		name     string
		node     htmlFixtureNode
		expected string
	}{
		{name: "horizontal layout", node: element("vaadin-horizontal-layout", text("caption"), field("vaadin-text-field", "readable")), expected: "<div>captionreadable</div>"},
		{name: "mismatched columns", node: htmlFixtureNode{tag: "div", classes: []string{"flex-md-row"}, children: []htmlFixtureNode{element("div", element("label", text("caption"))), element("div", field("vaadin-text-field", "readable"))}}, expected: "<div><div>caption</div><div>readable</div></div>"},
		{name: "absent label", node: rowColumns(element("div"), column("col-md-8", field("vaadin-text-field", "readable"))), expected: "<div><div>readable</div></div>"},
		{name: "unrelated caption content", node: rowColumns(column("col-md-4", element("label", text("caption")), text("extra")), column("col-md-8", field("vaadin-text-field", "readable"))), expected: "<div><div>captionextra</div><div>readable</div></div>"},
		{name: "editable value", node: informationRow("caption", htmlFixtureNode{tag: "vaadin-text-field", properties: map[string]any{"value": "secret"}, children: []htmlFixtureNode{text("secret")}}), expected: "<div><div>caption</div></div>"},
		{name: "active content beside value", node: informationRow("caption", field("vaadin-text-field", "readable"), element("script", text("secret"))), expected: "<div><div>caption</div><div>readable</div></div>"},
		{name: "three columns", node: rowColumns(column("col-md-4", element("label", text("caption"))), column("col-md-8", field("vaadin-text-field", "readable")), element("div", text("extra"))), expected: "<div><div>caption</div><div>readable</div><div>extra</div></div>"},
		{name: "missing field value", node: informationRow("caption", htmlFixtureNode{tag: "vaadin-text-field", properties: map[string]any{"readonly": true}}), expected: "<div><div>caption</div></div>"},
		{name: "unavailable field value", node: informationRow("caption", htmlFixtureNode{tag: "vaadin-text-field", properties: map[string]any{"readonly": true, "value": 42}}), expected: "<div><div>caption</div></div>"},
		{name: "nested candidate row", node: informationRow("caption", informationRow("inner", field("vaadin-text-field", "readable"))), expected: "<div><div>caption</div>" + tableHTML("<tr><td><strong>inner</strong></td><td>readable</td></tr>") + "</div>"},
	} {
		test.Run("should use safe fallback when row has "+scenario.name, func(test *testing.T) {
			// Given
			tree, roots := htmlTree(test, scenario.node)

			// When
			actual, err := vaadin.RenderHTML(tree, roots...)

			// Then
			assert.NoError(test, err)
			assert.Equal(test, scenario.expected, actual)
		})
	}
	test.Run("should retain semantic emphasis when row and columns carry styles", func(test *testing.T) {
		// Given
		source := informationRow("caption", field("vaadin-text-field", "readable"))
		source.styles = map[string]any{"font-style": "italic"}
		source.children[1].styles = map[string]any{"font-weight": "700"}
		tree, roots := htmlTree(test, source)

		// When
		actual, err := vaadin.RenderHTML(tree, roots...)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, tableHTML("<tr><td><strong><em>caption</em></strong></td><td><strong><em>readable</em></strong></td></tr>"), actual)
	})
	test.Run("should recognize either breakpoint when hidden and whitespace siblings exist", func(test *testing.T) {
		// Given
		source := rowColumns(
			column("col-lg-3", element("div", element("label", text("caption")))),
			column("col-lg-9", field("vaadin-text-field", "readable")),
		)
		source.children = append([]htmlFixtureNode{text(" "), {tag: "div", attributes: map[string]any{"hidden": ""}}}, source.children...)
		tree, roots := htmlTree(test, source)

		// When
		actual, err := vaadin.RenderHTML(tree, roots...)

		// Then
		assert.NoError(test, err)
		assert.Equal(test, tableHTML("<tr><td><strong>caption</strong></td><td>readable</td></tr>"), actual)
	})

	test.Run("should preserve output when IDs and class order change", func(test *testing.T) {
		// Given
		source := informationRow("caption", field("vaadin-text-field", "readable"))
		original, originalRoots := htmlTree(test, source)
		source.classes = []string{"extra", "flex-md-row"}
		source.children[0].classes = []string{"extra", "col-lg-3", "col-md-4"}
		source.children[1].classes = []string{"col-md-8", "extra", "col-lg-9"}
		remapped, remappedRoots := htmlTree(test, text("unselected ID offset"), source)

		// When
		actual, err := vaadin.RenderHTML(original, originalRoots...)
		changed, changedError := vaadin.RenderHTML(remapped, remappedRoots[1])

		// Then
		assert.NoError(test, err)
		assert.NoError(test, changedError)
		assert.Equal(test, actual, changed)
		assert.Equal(test, 1, strings.Count(actual, "<table>"))
	})
}

// -- Internals ----------------------------------------------------------------

type htmlFixtureNode struct {
	tag, text                      string
	properties, attributes, styles map[string]any
	classes                        []string
	children                       []htmlFixtureNode
}

func element(tag string, children ...htmlFixtureNode) htmlFixtureNode {
	return htmlFixtureNode{tag: tag, children: children}
}
func text(value string) htmlFixtureNode { return htmlFixtureNode{text: value} }
func field(tag, value string) htmlFixtureNode {
	return htmlFixtureNode{tag: tag, properties: map[string]any{"readonly": true, "value": value}}
}
func link(href string, children ...htmlFixtureNode) htmlFixtureNode {
	return htmlFixtureNode{tag: "a", attributes: map[string]any{"href": href, "onclick": "malicious()", "class": "ignored", "style": "ignored"}, children: children}
}

func htmlTree(test *testing.T, nodes ...htmlFixtureNode) (*vaadin.StateTree, []vaadin.Node) {
	test.Helper()
	changes := []vaadin.Change{}
	nextID := 0
	var appendNode func(htmlFixtureNode) vaadin.NodeID
	appendNode = func(source htmlFixtureNode) vaadin.NodeID {
		nextID++
		id := vaadin.NodeID(nextID)
		changes = append(changes, vaadin.Change{"node": float64(id), "type": vaadin.Attach})
		features := map[vaadin.FeatureID]map[string]any{vaadin.PropertiesFeature: source.properties, vaadin.AttributesFeature: source.attributes, vaadin.StylesFeature: source.styles}
		if source.tag != "" {
			features[vaadin.ElementFeature] = map[string]any{"tag": source.tag}
		}
		if source.text != "" {
			features[vaadin.TextFeature] = map[string]any{"text": source.text}
		}
		for feature, properties := range features {
			for key, value := range properties {
				changes = append(changes, vaadin.Change{"node": float64(id), "type": vaadin.Put, "feat": float64(feature), "key": key, "value": value})
			}
		}
		classes := []any{}
		for _, class := range source.classes {
			classes = append(classes, class)
		}
		changes = append(changes, vaadin.Change{"node": float64(id), "type": vaadin.Splice, "feat": float64(vaadin.ClassesFeature), "index": float64(0), "add": classes})
		children := []any{}
		for _, child := range source.children {
			children = append(children, float64(appendNode(child)))
		}
		changes = append(changes, vaadin.Change{"node": float64(id), "type": vaadin.Splice, "feat": float64(vaadin.ChildrenFeature), "index": float64(0), "addNodes": children})
		return id
	}
	ids := []vaadin.NodeID{}
	for _, node := range nodes {
		ids = append(ids, appendNode(node))
	}
	tree := vaadin.NewStateTree()
	assert.NoError(test, tree.Apply(changes))
	roots := []vaadin.Node{}
	for _, id := range ids {
		root, exists := tree.FindByID(id)
		assert.True(test, exists)
		roots = append(roots, root)
	}
	return tree, roots
}
