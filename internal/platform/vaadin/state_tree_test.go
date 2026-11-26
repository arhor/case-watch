package vaadin_test

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/arhor/case-watch/internal/platform/vaadin"
	"github.com/arhor/case-watch/internal/testing/assert"
)

func TestStateTreeApply(test *testing.T) {
	test.Run("should preserve feature types and presence when property keys collide", func(test *testing.T) {
		// Given
		tree := vaadin.NewStateTree()
		changes := decodeChanges(test, `[
   {"node":1,"type":"put","feat":0,"key":"tag","value":"body"},
   {"node":1,"type":"put","feat":0,"key":"visible","value":false},
   {"node":1,"type":"put","feat":0,"key":"value","value":"element"},
   {"node":1,"type":"put","feat":1,"key":"value","value":""},
   {"node":1,"type":"put","feat":1,"key":"readonly","value":false},
   {"node":1,"type":"put","feat":1,"key":"number","value":2},
   {"node":1,"type":"put","feat":1,"key":"reference","nodeValue":2},
   {"node":1,"type":"put","feat":1,"key":"null","value":null},
   {"node":1,"type":"put","feat":3,"key":"value","value":"attribute"},
   {"node":1,"type":"put","feat":7,"key":"value","value":"text-feature"},
   {"node":1,"type":"put","feat":12,"key":"value","value":"style"},
   {"node":2,"type":"attach"},
   {"node":2,"type":"put","feat":7,"key":"text","value":""}
  ]`)

		// When
		err := tree.Apply(changes)
		node, found := tree.FindByID(1)
		textNode, textFound := tree.FindByID(2)

		// Then
		assert.NoError(test, err)
		assert.True(test, found && textFound)
		assert.Equal(test, "body", node.Tag())
		visible, present := node.Visible()
		assert.Equal(test, false, visible)
		assert.True(test, present)
		for feature, expected := range map[vaadin.FeatureID]string{vaadin.ElementFeature: "element", vaadin.PropertiesFeature: "", vaadin.AttributesFeature: "attribute", vaadin.TextFeature: "text-feature", vaadin.StylesFeature: "style"} {
			value, exists := node.Property(feature, "value")
			assert.True(test, exists)
			assert.Equal[any](test, expected, value)
		}
		assert.DeepEqual(test, map[string]any{"value": "", "readonly": false, "number": float64(2), "reference": vaadin.NodeReference{ID: 2}, "null": nil}, node.Properties(vaadin.PropertiesFeature))
		_, present = node.Property(vaadin.PropertiesFeature, "absent")
		assert.True(test, !present)
		text, present := textNode.Text()
		assert.True(test, present)
		assert.Equal(test, "", text)
		assert.Equal(test, "", textNode.Tag())
	})
	test.Run("should update and remove only addressed properties when maps are mutated", func(test *testing.T) {
		// Given
		tree := vaadin.NewStateTree()
		assert.NoError(test, tree.Apply(decodeChanges(test, `[
   {"node":1,"type":"put","feat":1,"key":"value","value":"old"},
   {"node":1,"type":"put","feat":3,"key":"value","value":"attribute"},
   {"node":1,"type":"put","feat":12,"key":"color","value":"red"}
  ]`)))
		previous, _ := tree.FindByID(1)
		changes := decodeChanges(test, `[
   {"node":1,"type":"put","feat":1,"key":"value","value":"new"},
   {"node":1,"type":"remove","feat":3,"key":"value"},
   {"node":1,"type":"remove","feat":12,"key":"color"},
   {"node":1,"type":"remove","feat":1,"key":"missing"}
  ]`)

		// When
		err := tree.Apply(changes)
		current, _ := tree.FindByID(1)

		// Then
		assert.NoError(test, err)
		assert.True(test, previous == current, "node handles keep their identity")
		assert.DeepEqual(test, map[string]any{"value": "new"}, current.Properties(vaadin.PropertiesFeature))
		assert.Equal(test, 0, len(current.Properties(vaadin.AttributesFeature)))
		assert.Equal(test, 0, len(current.Properties(vaadin.StylesFeature)))
	})
	test.Run("should retain descendants when child links and an old parent are removed", func(test *testing.T) {
		// Given
		tree := vaadin.NewStateTree()
		assert.NoError(test, tree.Apply(decodeChanges(test, `[
   {"node":1,"type":"splice","feat":2,"index":0,"addNodes":[2]},
   {"node":2,"type":"attach"},
   {"node":2,"type":"splice","feat":2,"index":0,"addNodes":[3]},
   {"node":3,"type":"attach"}
  ]`)))
		changes := decodeChanges(test, `[
   {"node":1,"type":"splice","feat":2,"index":0,"remove":1},
   {"node":1,"type":"splice","feat":2,"index":0,"addNodes":[4]},
   {"node":4,"type":"attach"},
   {"node":2,"type":"detach"}
  ]`)

		// When
		err := tree.Apply(changes)
		root, _ := tree.FindByID(1)
		_, oldParent := tree.FindByID(2)
		_, descendant := tree.FindByID(3)

		// Then
		assert.NoError(test, err)
		assert.DeepEqual(test, []vaadin.NodeID{4}, root.ChildIDs())
		assert.True(test, !oldParent)
		assert.True(test, descendant)
	})
	for _, scenario := range []struct {
		name, change string
		children     []vaadin.NodeID
		classes      []string
	}{
		{name: "insert children in middle", change: `{"node":1,"type":"splice","feat":2,"index":1,"addNodes":[5]}`, children: []vaadin.NodeID{2, 5, 3, 4}, classes: []string{"a", "b", "c"}},
		{name: "replace children", change: `{"node":1,"type":"splice","feat":2,"index":1,"remove":2,"addNodes":[5]}`, children: []vaadin.NodeID{2, 5}, classes: []string{"a", "b", "c"}},
		{name: "remove children only", change: `{"node":1,"type":"splice","feat":2,"index":0,"remove":2}`, children: []vaadin.NodeID{4}, classes: []string{"a", "b", "c"}},
		{name: "append children", change: `{"node":1,"type":"splice","feat":2,"index":3,"addNodes":[5]}`, children: []vaadin.NodeID{2, 3, 4, 5}, classes: []string{"a", "b", "c"}},
		{name: "clear children", change: `{"node":1,"type":"clear","feat":2}`, children: []vaadin.NodeID{}, classes: []string{"a", "b", "c"}},
		{name: "insert classes in middle", change: `{"node":1,"type":"splice","feat":11,"index":1,"add":["x","y"]}`, children: []vaadin.NodeID{2, 3, 4}, classes: []string{"a", "x", "y", "b", "c"}},
		{name: "replace classes", change: `{"node":1,"type":"splice","feat":11,"index":1,"remove":2,"add":["x"]}`, children: []vaadin.NodeID{2, 3, 4}, classes: []string{"a", "x"}},
		{name: "remove classes only", change: `{"node":1,"type":"splice","feat":11,"index":0,"remove":3}`, children: []vaadin.NodeID{2, 3, 4}, classes: []string{}},
		{name: "clear classes", change: `{"node":1,"type":"clear","feat":11}`, children: []vaadin.NodeID{2, 3, 4}, classes: []string{}},
		{name: "declare an empty additional feature", change: `{"node":1,"type":"empty","feat":19,"featType":true}`, children: []vaadin.NodeID{2, 3, 4}, classes: []string{"a", "b", "c"}},
	} {
		test.Run("should preserve other features when changes "+scenario.name, func(test *testing.T) {
			// Given
			tree := seedTree(test)
			changes := decodeChanges(test, "["+scenario.change+"]")

			// When
			err := tree.Apply(changes)
			root, _ := tree.FindByID(1)

			// Then
			assert.NoError(test, err)
			assert.DeepEqual(test, scenario.children, root.ChildIDs())
			assert.DeepEqual(test, scenario.classes, root.Classes())
			assert.Equal(test, "body", root.Tag())
			assert.Equal(test, 5, len(tree.Nodes()), "unlinking does not detach nodes")
		})
	}
	for _, scenario := range []struct{ name, change string }{
		{name: "negative node", change: `{"node":-1,"type":"attach"}`},
		{name: "fractional node", change: `{"node":1.5,"type":"attach"}`},
		{name: "unsafe node", change: `{"node":9007199254740992,"type":"attach"}`},
		{name: "string node", change: `{"node":"example-secret","type":"attach"}`},
		{name: "null node", change: `{"node":null,"type":"attach"}`},
		{name: "missing node", change: `{"type":"attach"}`},
		{name: "unknown operation on ignored feature", change: `{"node":1,"type":"example-secret","feat":4}`},
		{name: "missing operation", change: `{"node":1}`},
		{name: "null operation", change: `{"node":1,"type":null}`},
		{name: "missing feature", change: `{"node":1,"type":"clear"}`},
		{name: "fractional feature", change: `{"node":1,"type":"clear","feat":2.5}`},
		{name: "unknown feature", change: `{"node":1,"type":"clear","feat":999}`},
		{name: "unsupported map operation", change: `{"node":1,"type":"splice","feat":1,"index":0}`},
		{name: "unsupported child operation", change: `{"node":1,"type":"put","feat":2,"key":"value","value":3}`},
		{name: "unsupported class operation", change: `{"node":1,"type":"remove","feat":11,"key":"value"}`},
		{name: "missing key", change: `{"node":1,"type":"put","feat":0,"value":"example-secret"}`},
		{name: "null remove key", change: `{"node":1,"type":"remove","feat":1,"key":null}`},
		{name: "missing value", change: `{"node":1,"type":"put","feat":1,"key":"value"}`},
		{name: "mixed value and reference", change: `{"node":1,"type":"put","feat":1,"key":"value","value":2,"nodeValue":2}`},
		{name: "invalid property reference", change: `{"node":1,"type":"put","feat":1,"key":"value","nodeValue":"2"}`},
		{name: "dangling property reference", change: `{"node":1,"type":"put","feat":1,"key":"value","nodeValue":999}`},
		{name: "missing index", change: `{"node":1,"type":"splice","feat":2}`},
		{name: "negative index", change: `{"node":1,"type":"splice","feat":2,"index":-1}`},
		{name: "fractional index", change: `{"node":1,"type":"splice","feat":2,"index":1.2}`},
		{name: "index beyond end", change: `{"node":1,"type":"splice","feat":2,"index":4}`},
		{name: "negative removal", change: `{"node":1,"type":"splice","feat":2,"index":0,"remove":-1}`},
		{name: "null removal", change: `{"node":1,"type":"splice","feat":2,"index":0,"remove":null}`},
		{name: "string removal", change: `{"node":1,"type":"splice","feat":2,"index":0,"remove":"1"}`},
		{name: "oversized removal", change: `{"node":1,"type":"splice","feat":2,"index":1,"remove":3}`},
		{name: "scalar child additions", change: `{"node":1,"type":"splice","feat":2,"index":0,"add":[2]}`},
		{name: "null child additions", change: `{"node":1,"type":"splice","feat":2,"index":0,"addNodes":null}`},
		{name: "fractional child reference", change: `{"node":1,"type":"splice","feat":2,"index":0,"addNodes":[2.2]}`},
		{name: "dangling child", change: `{"node":1,"type":"splice","feat":2,"index":0,"addNodes":[999]}`},
		{name: "duplicate child", change: `{"node":1,"type":"splice","feat":2,"index":0,"addNodes":[2]}`},
		{name: "multiple parents", change: `{"node":5,"type":"splice","feat":2,"index":0,"addNodes":[2]}`},
		{name: "cycle", change: `{"node":2,"type":"splice","feat":2,"index":0,"addNodes":[1]}`},
		{name: "disconnected self cycle", change: `{"node":5,"type":"splice","feat":2,"index":0,"addNodes":[5]}`},
		{name: "detach still linked node", change: `{"node":2,"type":"detach"}`},
		{name: "node additions for classes", change: `{"node":1,"type":"splice","feat":11,"index":0,"addNodes":[2]}`},
		{name: "numeric class", change: `{"node":1,"type":"splice","feat":11,"index":0,"add":[2]}`},
		{name: "string additions", change: `{"node":1,"type":"splice","feat":11,"index":0,"add":"example-secret"}`},
		{name: "invalid empty declaration", change: `{"node":1,"type":"empty","feat":19,"featType":null}`},
		{name: "unsupported additional operation", change: `{"node":1,"type":"clear","feat":19}`},
	} {
		test.Run("should leave prior state intact when mutation has "+scenario.name, func(test *testing.T) {
			// Given
			tree := seedTree(test)
			root, _ := tree.FindByID(1)
			changes := decodeChanges(test, `[{"node":1,"type":"put","feat":0,"key":"tag","value":"example-secret"},`+scenario.change+`]`)

			// When
			err := tree.Apply(changes)
			current, _ := tree.FindByID(1)

			// Then
			assert.True(test, err != nil)
			assert.True(test, !strings.Contains(err.Error(), "example-secret"))
			assert.True(test, root == current)
			assert.Equal(test, "body", root.Tag())
			assert.DeepEqual(test, []vaadin.NodeID{2, 3, 4}, root.ChildIDs())
			assert.DeepEqual(test, []string{"a", "b", "c"}, root.Classes())
			assert.Equal(test, 5, len(tree.Nodes()))
		})
	}
	test.Run("should isolate mutable snapshots when callers modify values after application", func(test *testing.T) {
		// Given
		tree := seedTree(test)
		changes := decodeChanges(test, `[{"node":1,"type":"put","feat":1,"key":"nested","value":{"items":["original"]}}]`)
		assert.NoError(test, tree.Apply(changes))
		node, _ := tree.FindByID(1)

		// When
		changes[0]["value"].(map[string]any)["items"].([]any)[0] = "changed input"
		properties := node.Properties(vaadin.PropertiesFeature)
		properties["nested"].(map[string]any)["items"].([]any)[0] = "changed map"
		value, _ := node.Property(vaadin.PropertiesFeature, "nested")
		value.(map[string]any)["items"].([]any)[0] = "changed property"
		children := node.ChildIDs()
		children[0] = 999
		classes := node.Classes()
		classes[0] = "changed class"
		nodes := tree.Nodes()
		nodes[0] = nil

		// Then
		assert.DeepEqual(test, map[string]any{"nested": map[string]any{"items": []any{"original"}}}, node.Properties(vaadin.PropertiesFeature))
		assert.DeepEqual(test, []vaadin.NodeID{2, 3, 4}, node.ChildIDs())
		assert.DeepEqual(test, []string{"a", "b", "c"}, node.Classes())
		current, exists := tree.FindByID(1)
		assert.True(test, exists && current == node)
	})
	for _, flow := range []string{"authenticated", "rpc"} {
		test.Run("should reconstruct independently verified UI when replaying "+flow+" fixtures", func(test *testing.T) {
			// Given
			var reference struct {
				Tree     referenceNode              `json:"tree"`
				Excluded map[string][]vaadin.NodeID `json:"excludedBranches"`
			}
			assert.NoError(test, json.Unmarshal(fixture(test, "expected-state-tree.json"), &reference))
			file := "bootstrap-authenticated.html"
			if flow == "rpc" {
				file = "bootstrap-login.html"
			}
			bootstrap, err := vaadin.DecodeBootstrap(fixture(test, file))
			assert.NoError(test, err)
			tree := vaadin.NewStateTree()

			// When
			err = tree.Apply(bootstrap.Message.Changes)
			assert.NoError(test, err)
			if flow == "rpc" {
				var responses []string
				assert.NoError(test, json.Unmarshal(fixture(test, "rpc-authentication.json"), &responses))
				assert.Equal(test, 4, len(responses))
				for _, response := range responses {
					messages, err := vaadin.DecodeResponse([]byte(response))
					assert.NoError(test, err)
					for _, message := range messages {
						assert.NoError(test, tree.Apply(message.Changes))
					}
				}
			}
			before := tree.Nodes()
			channel, channelErr := vaadin.DecodeResponse(fixture(test, "rpc-channel.txt"))
			for _, message := range channel {
				assert.NoError(test, tree.Apply(message.Changes))
			}

			// Then
			assert.NoError(test, channelErr)
			assert.DeepEqual(test, before, tree.Nodes())
			root, exists := tree.FindByID(reference.Tree.IDs[flow])
			assert.True(test, exists)
			assert.Equal(test, 48, assertReference(test, tree, root, reference.Tree, reference.Excluded[flow]))
		})
	}
}

func TestStateTreeRoots(test *testing.T) {
	test.Run("should expose logical roots when detached and non-DOM nodes exist", func(test *testing.T) {
		// Given
		tree := seedTree(test)
		changes := decodeChanges(test, `[
   {"node":2,"type":"put","feat":0,"key":"tag","value":"div"},
   {"node":5,"type":"put","feat":7,"key":"text","value":"orphan"},
   {"node":6,"type":"attach"},
   {"node":6,"type":"put","feat":4,"key":"listener","value":"ignored"},
   {"node":7,"type":"put","feat":0,"key":"tag","value":"detached"},
   {"node":7,"type":"detach"}
  ]`)
		assert.NoError(test, tree.Apply(changes))

		// When
		roots := tree.Roots()
		all := tree.Nodes()

		// Then
		assert.Equal(test, 2, len(roots))
		assert.Equal(test, vaadin.NodeID(1), roots[0].ID())
		assert.Equal(test, vaadin.NodeID(5), roots[1].ID())
		assert.Equal(test, 6, len(all))
		assert.Equal(test, vaadin.NodeID(6), all[5].ID())
	})
}

func TestStateTreeSubtree(test *testing.T) {
	test.Run("should traverse in preorder when child order differs from node IDs", func(test *testing.T) {
		// Given
		tree := seedTree(test)
		assert.NoError(test, tree.Apply(decodeChanges(test, `[
   {"node":1,"type":"splice","feat":2,"index":0,"remove":3,"addNodes":[4,2]},
   {"node":4,"type":"splice","feat":2,"index":0,"addNodes":[3]}
  ]`)))

		// When
		subtree := tree.Subtree(1)
		missing := tree.Subtree(999)
		subtreeIDs := []vaadin.NodeID{}
		for _, node := range subtree {
			subtreeIDs = append(subtreeIDs, node.ID())
		}

		// Then
		assert.DeepEqual(test, []vaadin.NodeID{1, 4, 3, 2}, subtreeIDs)
		assert.Equal(test, 0, len(missing))
	})
}

// -- Internals ----------------------------------------------------------------

func decodeChanges(test *testing.T, data string) []vaadin.Change {
	test.Helper()
	messages, err := vaadin.DecodeResponse([]byte(`[{"syncId":0,"clientId":0,"changes":` + data + `}]`))
	assert.NoError(test, err)
	return messages[0].Changes
}

func seedTree(test *testing.T) *vaadin.StateTree {
	test.Helper()
	tree := vaadin.NewStateTree()
	assert.NoError(test, tree.Apply(decodeChanges(test, `[
  {"node":1,"type":"put","feat":0,"key":"tag","value":"body"},
  {"node":1,"type":"splice","feat":2,"index":0,"addNodes":[2,3,4]},
  {"node":1,"type":"splice","feat":11,"index":0,"add":["a","b","c"]},
  {"node":2,"type":"attach"},{"node":3,"type":"attach"},{"node":4,"type":"attach"},{"node":5,"type":"attach"}
 ]`)))
	return tree
}

func fixture(test *testing.T, name string) []byte {
	test.Helper()
	data, err := os.ReadFile("../../adapters/outbound/clients/testdata/vaadin/" + name)
	assert.NoError(test, err)
	return data
}

type referenceNode struct {
	IDs        map[string]vaadin.NodeID `json:"ids"`
	Tag        string                   `json:"tag"`
	Text       *string                  `json:"text"`
	Properties map[string]any           `json:"properties"`
	Classes    []string                 `json:"classes"`
	Styles     map[string]any           `json:"styles"`
	Children   []referenceNode          `json:"children"`
}

func assertReference(test *testing.T, tree *vaadin.StateTree, node *vaadin.Node, reference referenceNode, excluded []vaadin.NodeID) int {
	test.Helper()
	assert.Equal(test, reference.Tag, node.Tag(), fmt.Sprintf("node %d", node.ID()))
	text, present := node.Text()
	assert.Equal(test, reference.Text != nil, present)
	if reference.Text != nil {
		assert.Equal(test, *reference.Text, text)
	}
	expectedProperties := reference.Properties
	if expectedProperties == nil {
		expectedProperties = map[string]any{}
	}
	assert.DeepEqual(test, expectedProperties, node.Properties(vaadin.PropertiesFeature))
	expectedStyles := reference.Styles
	if expectedStyles == nil {
		expectedStyles = map[string]any{}
	}
	assert.DeepEqual(test, expectedStyles, node.Properties(vaadin.StylesFeature))
	expectedClasses := append([]string{}, reference.Classes...)
	actualClasses := node.Classes()
	slices.Sort(expectedClasses)
	slices.Sort(actualClasses)
	assert.DeepEqual(test, expectedClasses, actualClasses)
	actualChildren := []*vaadin.Node{}
	for _, child := range tree.Children(node.ID()) {
		if !slices.Contains(excluded, child.ID()) {
			actualChildren = append(actualChildren, child)
		}
	}
	assert.Equal(test, len(reference.Children), len(actualChildren))
	count := 1
	for index, child := range reference.Children {
		count += assertReference(test, tree, actualChildren[index], child, excluded)
	}
	return count
}
