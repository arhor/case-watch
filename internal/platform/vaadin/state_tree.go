package vaadin

import (
	"errors"
	"sort"
)

// StateTree belongs to one UI/session. It is not safe for concurrent mutation.
// The zero value is an empty tree.
type StateTree struct {
	nodes map[NodeID]*nodeState
}

func NewStateTree() *StateTree { return &StateTree{nodes: map[NodeID]*nodeState{}} }

// Apply processes a complete message in order, allowing forward references
// within that message. Invalid mutations leave the entire prior tree unchanged.
func (this *StateTree) Apply(changes []Change) error {
	pending := NewStateTree()
	retainedRegistrations := map[NodeID]*nodeState{}
	for id, node := range this.nodes {
		handle := Node{state: node}
		nodeCopy := &nodeState{id: id, features: map[FeatureID]map[string]any{}, children: handle.ChildIDs(), classes: handle.Classes()}
		for feature := range node.features {
			nodeCopy.features[feature] = handle.Properties(feature)
		}
		pending.nodes[id] = nodeCopy
		retainedRegistrations[id] = nodeCopy
	}
	for _, change := range changes {
		if err := pending.apply(change); err != nil {
			return err
		}
	}
	if err := pending.validate(); err != nil {
		return err
	}
	for id, node := range pending.nodes {
		// A detach followed by registration creates a new lifetime for that ID.
		// Only surviving registrations may update previously returned handles.
		if existing, ok := this.nodes[id]; ok && node == retainedRegistrations[id] {
			*existing = *node
			pending.nodes[id] = existing
		}
	}
	this.nodes = pending.nodes
	return nil
}

func (this *StateTree) FindByID(id NodeID) (Node, bool) {
	node, exists := this.nodes[id]
	return Node{state: node}, exists
}

// Nodes includes non-DOM protocol nodes in deterministic ID order.
func (this *StateTree) Nodes() []Node {
	nodes := make([]Node, 0, len(this.nodes))
	for _, node := range this.nodes {
		nodes = append(nodes, Node{state: node})
	}
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].ID() < nodes[right].ID() })
	return nodes
}

// Roots returns parentless DOM nodes (element or text features), including the
// implicit document root. Detached nodes are absent, and inert protocol nodes
// without DOM features are excluded. Multiple roots are ordered by ID.
func (this *StateTree) Roots() []Node {
	parents := map[NodeID]bool{}
	for _, node := range this.nodes {
		for _, child := range node.children {
			parents[child] = true
		}
	}
	roots := []Node{}
	for _, node := range this.Nodes() {
		_, element := node.state.features[ElementFeature]
		_, text := node.state.features[TextFeature]
		if !parents[node.ID()] && (element || text) {
			roots = append(roots, node)
		}
	}
	return roots
}

func (this *StateTree) Children(id NodeID) []Node {
	children := []Node{}
	if node, exists := this.nodes[id]; exists {
		for _, childID := range node.children {
			if child, exists := this.nodes[childID]; exists {
				children = append(children, Node{state: child})
			}
		}
	}
	return children
}

// Subtree returns preorder traversal, preserving child order. The iterative
// traversal also guards against repeated references and unbounded recursion.
func (this *StateTree) Subtree(id NodeID) []Node {
	result := []Node{}
	seen := map[NodeID]bool{}
	pending := []NodeID{id}
	for len(pending) > 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		node, exists := this.nodes[current]
		if !exists || seen[current] {
			continue
		}
		seen[current] = true
		result = append(result, Node{state: node})
		for index := len(node.children) - 1; index >= 0; index-- {
			pending = append(pending, node.children[index])
		}
	}
	return result
}

// -- Internals ----------------------------------------------------------------

func (this *StateTree) apply(change Change) error {
	id, ok := integer(change["node"])
	if !ok {
		return errors.New("invalid RPC node")
	}
	operation, ok := change["type"].(string)
	if !ok {
		return errors.New("invalid RPC operation")
	}
	switch operation {
	case Attach, Detach, Put, Remove, Splice, Clear, Empty:
	default:
		return errors.New("unsupported RPC operation")
	}
	if operation == Detach {
		delete(this.nodes, NodeID(id))
		return nil
	}
	node := this.nodes[NodeID(id)]
	if node == nil {
		// Mutation targets may be implicitly registered (notably the body root).
		node = &nodeState{id: NodeID(id), features: map[FeatureID]map[string]any{}}
		this.nodes[node.id] = node
	}
	if operation == Attach {
		return nil
	}
	featureNumber, ok := integer(change["feat"])
	if !ok {
		return errors.New("invalid RPC feature")
	}
	feature := FeatureID(featureNumber)
	switch feature {
	case ListenersFeature, PushFeature, PushParametersFeature:
		// Listener/push mutations are explicitly ignored, after checking the op.
		return nil
	case AdditionalFeature:
		if operation != Empty {
			return errors.New("unsupported additional feature operation")
		}
		if _, ok := change["featType"].(bool); !ok {
			return errors.New("invalid empty feature declaration")
		}
		return nil
	case ChildrenFeature, ClassesFeature:
		return applyList(node, feature, operation, change)
	case ElementFeature, PropertiesFeature, AttributesFeature, TextFeature, StylesFeature:
		return applyMap(node, feature, operation, change)
	default:
		return errors.New("unsupported RPC feature")
	}
}

func applyMap(node *nodeState, feature FeatureID, operation string, change Change) error {
	if operation != Put && operation != Remove {
		return errors.New("unsupported map operation")
	}
	key, ok := change["key"].(string)
	if !ok {
		return errors.New("invalid property key")
	}
	if operation == Remove {
		delete(node.features[feature], key)
		return nil
	}
	value, hasValue := change["value"]
	reference, hasReference := change["nodeValue"]
	if hasValue == hasReference {
		return errors.New("invalid property value")
	}
	if hasReference {
		id, ok := integer(reference)
		if !ok {
			return errors.New("invalid property reference")
		}
		value = NodeReference{ID: NodeID(id)}
	}
	if node.features[feature] == nil {
		node.features[feature] = map[string]any{}
	}
	node.features[feature][key] = cloneValue(value)
	return nil
}

func applyList(node *nodeState, feature FeatureID, operation string, change Change) error {
	if operation == Clear {
		if feature == ChildrenFeature {
			node.children = nil
		} else {
			node.classes = nil
		}
		return nil
	}
	if operation != Splice {
		return errors.New("unsupported list operation")
	}
	index, indexOK := integer(change["index"])
	var removal uint64
	if value, exists := change["remove"]; exists {
		var ok bool
		removal, ok = integer(value)
		if !ok {
			return errors.New("invalid splice removal")
		}
	}
	length := len(node.children)
	additionKey, wrongKey := "addNodes", "add"
	if feature == ClassesFeature {
		length = len(node.classes)
		additionKey, wrongKey = wrongKey, additionKey
	}
	if !indexOK || index > uint64(length) || removal > uint64(length)-index {
		return errors.New("invalid splice range")
	}
	if _, exists := change[wrongKey]; exists {
		return errors.New("invalid splice payload")
	}
	var additions []any
	if value, exists := change[additionKey]; exists {
		var ok bool
		additions, ok = value.([]any)
		if !ok {
			return errors.New("invalid splice additions")
		}
	}
	if feature == ChildrenFeature {
		next := append([]NodeID{}, node.children[:int(index)]...)
		for _, value := range additions {
			id, ok := integer(value)
			if !ok {
				return errors.New("invalid child reference")
			}
			next = append(next, NodeID(id))
		}
		node.children = append(next, node.children[int(index+removal):]...)
	} else {
		next := append([]string{}, node.classes[:int(index)]...)
		for _, value := range additions {
			class, ok := value.(string)
			if !ok {
				return errors.New("invalid class value")
			}
			next = append(next, class)
		}
		node.classes = append(next, node.classes[int(index+removal):]...)
	}
	return nil
}

func (this *StateTree) validate() error {
	// Kahn's algorithm validates the entire registry, including disconnected
	// subtrees, without recursion. A child can have only one structural parent.
	parents := map[NodeID]int{}
	for _, node := range this.nodes {
		for _, child := range node.children {
			if _, exists := this.nodes[child]; !exists {
				return errors.New("dangling child reference")
			}
			parents[child]++
			if parents[child] > 1 {
				return errors.New("multiple child parents")
			}
		}
		for _, properties := range node.features {
			for _, value := range properties {
				if reference, ok := value.(NodeReference); ok {
					if _, exists := this.nodes[reference.ID]; !exists {
						return errors.New("dangling property reference")
					}
				}
			}
		}
	}
	pending := []NodeID{}
	for id := range this.nodes {
		if parents[id] == 0 {
			pending = append(pending, id)
		}
	}
	visited := 0
	for len(pending) > 0 {
		last := len(pending) - 1
		node := this.nodes[pending[last]]
		pending = pending[:last]
		visited++
		for _, child := range node.children {
			parents[child]--
			if parents[child] == 0 {
				pending = append(pending, child)
			}
		}
	}
	if visited != len(this.nodes) {
		return errors.New("cyclic child references")
	}
	return nil
}
