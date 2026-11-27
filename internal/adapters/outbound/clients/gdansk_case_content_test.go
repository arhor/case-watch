package clients

import (
	"os"
	"testing"

	"github.com/arhor/case-watch/internal/platform/vaadin"
	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestSelectGdanskCaseContent(test *testing.T) {
	test.Run("should select ordered informational branches and exclude the contact form", func(test *testing.T) {
		// Given
		tree := authenticatedGdanskTree(test)

		// When
		roots, err := selectGdanskCaseContent(tree, "Example stage")
		rootIDs := make([]vaadin.NodeID, 0, len(roots))
		for _, root := range roots {
			rootIDs = append(rootIDs, root.ID())
		}

		// Then
		assert.NoError(test, err)
		assert.DeepEqual(test, []vaadin.NodeID{29, 35, 41, 53, 59, 70}, rootIDs)
	})

	test.Run("should fail closed when one informational branch also contains interactive content", func(test *testing.T) {
		// Given
		tree := authenticatedGdanskTree(test)
		roots, err := selectGdanskCaseContent(tree, "Example stage")
		assert.NoError(test, err)
		branch := roots[0]
		assert.NoError(test, tree.Apply([]vaadin.Change{
			{"node": float64(90001), "type": vaadin.Attach},
			{"node": float64(90001), "type": vaadin.Put, "feat": float64(vaadin.ElementFeature), "key": "tag", "value": "button"},
			{"node": float64(branch.ID()), "type": vaadin.Splice, "feat": float64(vaadin.ChildrenFeature), "index": float64(len(branch.ChildIDs())), "addNodes": []any{float64(90001)}},
		}))

		// When
		selected, selectionError := selectGdanskCaseContent(tree, "Example stage")

		// Then
		assert.True(test, selectionError != nil, "mixed branch should make the selected fragment unavailable")
		assert.Equal(test, 0, len(selected))
	})
}

// -- Internals ----------------------------------------------------------------

func authenticatedGdanskTree(test *testing.T) *vaadin.StateTree {
	test.Helper()
	data, err := os.ReadFile("testdata/vaadin/bootstrap-authenticated.html")
	assert.NoError(test, err)
	bootstrap, err := vaadin.DecodeBootstrap(data)
	assert.NoError(test, err)
	tree := vaadin.NewStateTree()
	assert.NoError(test, tree.Apply(bootstrap.Message.Changes))
	return tree
}
