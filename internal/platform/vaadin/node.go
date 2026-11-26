package vaadin

// Node is a read-only value handle to private tree state. Copies compare equal
// while they refer to the same registration and observe its successful updates.
// Assigning a handle never changes the tree. Accessors copy mutable data.
// Removed handles retain their last committed snapshot, even if their numeric ID
// is registered again. The zero handle has no state and returns empty values.
type Node struct {
	state *nodeState
}

func (this Node) ID() NodeID {
	if this.state == nil {
		return 0
	}
	return this.state.id
}

func (this Node) Tag() string {
	value, _ := this.Property(ElementFeature, "tag")
	tag, _ := value.(string)
	return tag
}

func (this Node) Text() (string, bool) {
	value, _ := this.Property(TextFeature, "text")
	text, ok := value.(string)
	return text, ok
}

// Visible preserves absent versus explicitly false metadata. Rendering policy
// (including CSS visibility) belongs to the caller.
func (this Node) Visible() (bool, bool) {
	value, _ := this.Property(ElementFeature, "visible")
	visible, ok := value.(bool)
	return visible, ok
}

func (this Node) Property(feature FeatureID, key string) (any, bool) {
	if this.state == nil {
		return nil, false
	}
	value, exists := this.state.features[feature][key]
	return cloneValue(value), exists
}

func (this Node) Properties(feature FeatureID) map[string]any {
	properties := map[string]any{}
	if this.state == nil {
		return properties
	}
	for key, value := range this.state.features[feature] {
		properties[key] = cloneValue(value)
	}
	return properties
}

func (this Node) ChildIDs() []NodeID {
	if this.state == nil {
		return []NodeID{}
	}
	return append([]NodeID{}, this.state.children...)
}

func (this Node) Classes() []string {
	if this.state == nil {
		return []string{}
	}
	return append([]string{}, this.state.classes...)
}

// -- Internals ----------------------------------------------------------------

type nodeState struct {
	id       NodeID
	features map[FeatureID]map[string]any
	children []NodeID
	classes  []string
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, entry := range typed {
			result[key] = cloneValue(entry)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, entry := range typed {
			result[index] = cloneValue(entry)
		}
		return result
	default:
		return value
	}
}
