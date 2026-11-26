package vaadin

// Node is a read-only handle with stable identity while registered in its tree.
// Accessors return copies of mutable data. Removed handles retain their last
// snapshot; FindByID no longer returns them after detachment.
type Node struct {
	id       NodeID
	features map[FeatureID]map[string]any
	children []NodeID
	classes  []string
}

func (this *Node) ID() NodeID { return this.id }

func (this *Node) Tag() string {
	value, _ := this.Property(ElementFeature, "tag")
	tag, _ := value.(string)
	return tag
}

func (this *Node) Text() (string, bool) {
	value, _ := this.Property(TextFeature, "text")
	text, ok := value.(string)
	return text, ok
}

// Visible preserves absent versus explicitly false metadata. Rendering policy
// (including CSS visibility) belongs to the caller.
func (this *Node) Visible() (bool, bool) {
	value, _ := this.Property(ElementFeature, "visible")
	visible, ok := value.(bool)
	return visible, ok
}

func (this *Node) Property(feature FeatureID, key string) (any, bool) {
	value, exists := this.features[feature][key]
	return cloneValue(value), exists
}

func (this *Node) Properties(feature FeatureID) map[string]any {
	properties := make(map[string]any, len(this.features[feature]))
	for key, value := range this.features[feature] {
		properties[key] = cloneValue(value)
	}
	return properties
}

func (this *Node) ChildIDs() []NodeID { return append([]NodeID{}, this.children...) }

func (this *Node) Classes() []string { return append([]string{}, this.classes...) }

// -- Internals ----------------------------------------------------------------

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
