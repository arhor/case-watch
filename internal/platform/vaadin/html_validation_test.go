package vaadin

import (
	"testing"

	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestRenderHTMLValidation(test *testing.T) {
	for _, scenario := range []struct {
		name     string
		children []NodeID
	}{
		{name: "dangling child", children: []NodeID{999}},
		{name: "child cycle", children: []NodeID{1}},
		{name: "duplicate child", children: []NodeID{2, 2}},
	} {
		test.Run("should reject malformed structure when selected root has "+scenario.name, func(test *testing.T) {
			// Given
			tree := NewStateTree()
			// Corruption cannot be created through transactional Apply. Exercise the
			// renderer's defensive checks against invalid internal references directly.
			tree.nodes[1] = &nodeState{id: 1, children: scenario.children, features: map[FeatureID]map[string]any{ElementFeature: {"tag": "div"}}}
			tree.nodes[2] = &nodeState{id: 2, features: map[FeatureID]map[string]any{TextFeature: {"text": "private value"}}}
			root, _ := tree.FindByID(1)

			// When
			fragment, err := RenderHTML(tree, root)

			// Then
			assert.True(test, err != nil)
			assert.Equal(test, "", fragment)
		})
	}
	test.Run("should reject stale handle when node ID was reused", func(test *testing.T) {
		// Given
		tree := NewStateTree()
		assert.NoError(test, tree.Apply([]Change{{"node": float64(1), "type": Attach}}))
		stale, _ := tree.FindByID(1)
		assert.NoError(test, tree.Apply([]Change{{"node": float64(1), "type": Detach}, {"node": float64(1), "type": Attach}}))

		// When
		fragment, err := RenderHTML(tree, stale)

		// Then
		assert.True(test, err != nil)
		assert.Equal(test, "", fragment)
	})
	test.Run("should bound recursion when a valid tree is deeply nested", func(test *testing.T) {
		// Given
		tree := NewStateTree()
		for index := NodeID(1); index <= 514; index++ {
			children := []NodeID{}
			if index < 514 {
				children = append(children, index+1)
			}
			tree.nodes[index] = &nodeState{id: index, children: children, features: map[FeatureID]map[string]any{ElementFeature: {"tag": "div"}}}
		}
		root, _ := tree.FindByID(1)

		// When
		fragment, err := RenderHTML(tree, root)

		// Then
		assert.True(test, err != nil)
		assert.Equal(test, "", fragment)
	})
}
