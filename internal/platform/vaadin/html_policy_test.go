package vaadin_test

import (
	"testing"

	"github.com/arhor/case-watch/internal/platform/vaadin"
	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestIsHidden(test *testing.T) {
	for _, scenario := range []struct {
		name               string
		attributes, styles map[string]any
		metadata           any
		expected           bool
	}{
		{name: "absent visibility"},
		{name: "explicit false metadata", metadata: false, expected: true},
		{name: "explicit true metadata", metadata: true},
		{name: "non-boolean metadata", metadata: "false"},
		{name: "boolean hidden attribute", attributes: map[string]any{"hidden": "false"}, expected: true},
		{name: "aria hidden true", attributes: map[string]any{"aria-hidden": " TRUE "}, expected: true},
		{name: "aria hidden false", attributes: map[string]any{"aria-hidden": "false"}},
		{name: "display none", styles: map[string]any{"display": " NONE "}, expected: true},
		{name: "visibility hidden", styles: map[string]any{"visibility": " HIDDEN "}, expected: true},
		{name: "visibility collapse", styles: map[string]any{"visibility": "collapse"}, expected: true},
		{name: "visible display", styles: map[string]any{"display": "flex", "visibility": "visible"}},
	} {
		test.Run("should report own visibility when node has "+scenario.name, func(test *testing.T) {
			// Given
			tree, roots := htmlTree(test, htmlFixtureNode{tag: "div", attributes: scenario.attributes, styles: scenario.styles, children: []htmlFixtureNode{text("hidden subtree")}})
			if scenario.metadata != nil {
				assert.NoError(test, tree.Apply([]vaadin.Change{{"node": float64(roots[0].ID()), "type": vaadin.Put, "feat": float64(vaadin.ElementFeature), "key": "visible", "value": scenario.metadata}}))
			}

			// When
			hidden := vaadin.IsHidden(roots[0])
			childHidden := vaadin.IsHidden(tree.Children(roots[0].ID())[0])
			rendered, err := vaadin.RenderHTML(tree, roots...)

			// Then
			assert.Equal(test, scenario.expected, hidden)
			assert.True(test, !childHidden, "the predicate does not infer ancestor visibility")
			assert.NoError(test, err)
			expected := "<div>hidden subtree</div>"
			if scenario.expected {
				expected = ""
			}
			assert.Equal(test, expected, rendered)
		})
	}
}

func TestReadOnlyValue(test *testing.T) {
	for _, scenario := range []struct {
		name, tag  string
		properties map[string]any
		expected   string
		present    bool
	}{
		{name: "empty string", tag: "vaadin-text-field", properties: map[string]any{"readonly": true, "value": ""}, present: true},
		{name: "textarea value", tag: "vaadin-text-area", properties: map[string]any{"readonly": true, "value": " a\n "}, expected: " a\n ", present: true},
		{name: "missing value", tag: "vaadin-text-field", properties: map[string]any{"readonly": true, "placeholder": "secret"}},
		{name: "numeric value", tag: "vaadin-text-field", properties: map[string]any{"readonly": true, "value": 42}},
		{name: "null value", tag: "vaadin-text-field", properties: map[string]any{"readonly": true, "value": nil}},
		{name: "false readonly", tag: "vaadin-text-field", properties: map[string]any{"readonly": false, "value": "secret"}},
		{name: "missing readonly", tag: "vaadin-text-field", properties: map[string]any{"value": "secret"}},
		{name: "string readonly", tag: "vaadin-text-field", properties: map[string]any{"readonly": "true", "value": "secret"}},
		{name: "password field", tag: "vaadin-password-field", properties: map[string]any{"readonly": true, "value": "secret"}},
	} {
		test.Run("should preserve value availability when source has "+scenario.name, func(test *testing.T) {
			// Given
			tree, roots := htmlTree(test, htmlFixtureNode{tag: scenario.tag, properties: scenario.properties, children: []htmlFixtureNode{text("no fallback")}})

			// When
			value, present := vaadin.ReadOnlyValue(roots[0])
			excluded := vaadin.IsExcludedControl(roots[0])
			rendered, err := vaadin.RenderHTML(tree, roots...)

			// Then
			assert.Equal(test, scenario.expected, value)
			assert.Equal(test, scenario.present, present)
			assert.Equal(test, !scenario.present, excluded)
			assert.NoError(test, err)
			if !scenario.present {
				assert.Equal(test, "", rendered)
			}
		})
	}
}

func TestIsExcludedControl(test *testing.T) {
	for _, scenario := range []struct {
		name     string
		node     htmlFixtureNode
		excluded bool
	}{
		{name: "ordinary anchor", node: link("https://example.invalid", text("link"))},
		{name: "unknown passive component", node: element("vaadin-unknown", text("readable"))},
		{name: "editable div", node: htmlFixtureNode{tag: "div", attributes: map[string]any{"contenteditable": ""}}, excluded: true},
		{name: "readonly empty field", node: field("vaadin-text-field", "")},
		{name: "resource loading image", node: element("img"), excluded: true},
	} {
		test.Run("should report own exclusion when node is "+scenario.name, func(test *testing.T) {
			// Given
			_, roots := htmlTree(test, scenario.node)

			// When
			excluded := vaadin.IsExcludedControl(roots[0])

			// Then
			assert.Equal(test, scenario.excluded, excluded)
		})
	}
}
