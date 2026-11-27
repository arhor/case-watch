package clients

import (
	"errors"
	"strings"

	"github.com/arhor/case-watch/internal/platform/vaadin"
)

// -- Internals ----------------------------------------------------------------

const (
	gdanskSelectionMaxDepth = 512
	gdanskSelectionMaxNodes = 8192
	gdanskMaxAnchorPairs    = 16384
)

type gdanskTreeEntry struct {
	node   vaadin.Node
	parent vaadin.NodeID
	depth  int
}

type gdanskTreeVisit struct {
	node   vaadin.Node
	parent vaadin.NodeID
	depth  int
}

func selectGdanskCaseContent(tree *vaadin.StateTree, stage string) ([]vaadin.Node, error) {
	entries, err := attachedGdanskEntries(tree)
	if err != nil {
		return nil, err
	}
	entriesByID := make(map[vaadin.NodeID]gdanskTreeEntry, len(entries))
	var captions []vaadin.NodeID
	var stageFields []vaadin.NodeID
	for _, entry := range entries {
		entriesByID[entry.node.ID()] = entry
		if gdanskNodeHasText(entry.node, "Stage of your case") {
			captions = append(captions, entry.node.ID())
		}
		if entry.node.Tag() == "vaadin-text-field" {
			if value, ok := vaadin.ReadOnlyValue(entry.node); ok && value == stage && strings.TrimSpace(value) != "" {
				stageFields = append(stageFields, entry.node.ID())
			}
		}
	}
	if len(captions)*len(stageFields) > gdanskMaxAnchorPairs {
		return nil, errors.New("ambiguous stage anchor")
	}
	anchors := map[vaadin.NodeID]bool{}
	for _, captionID := range captions {
		for _, fieldID := range stageFields {
			if common, ok := lowestGdanskCommonAncestor(entriesByID, captionID, fieldID); ok {
				anchors[common] = true
			}
		}
	}
	if len(anchors) != 1 {
		return nil, errors.New("ambiguous stage anchor")
	}
	var anchor vaadin.NodeID
	for id := range anchors {
		anchor = id
	}
	var containers []vaadin.NodeID
	for id := anchor; id != 0; {
		entry := entriesByID[id]
		if entry.node.Tag() == "div" && hasGdanskContainerClasses(entry.node.Classes()) {
			containers = append(containers, id)
		}
		id = entry.parent
	}
	if len(containers) != 1 {
		return nil, errors.New("missing or ambiguous content container")
	}
	container := entriesByID[containers[0]].node
	var roots []vaadin.Node
	for _, branch := range tree.Children(container.ID()) {
		classification, classifyErr := classifyGdanskBranch(tree, branch)
		if classifyErr != nil {
			return nil, classifyErr
		}
		if classification == gdanskBranchMixed {
			return nil, errors.New("ambiguous content branch")
		}
		if classification == gdanskBranchInformation {
			roots = append(roots, branch)
		}
	}
	if len(roots) == 0 {
		return nil, errors.New("no informational content")
	}
	stageIncluded := false
	for _, root := range roots {
		if isGdanskDescendant(entriesByID, anchor, root.ID()) {
			stageIncluded = true
			break
		}
	}
	if !stageIncluded {
		return nil, errors.New("stage branch not selected")
	}
	return roots, nil
}

type gdanskBranchKind int

const (
	gdanskBranchExcluded gdanskBranchKind = iota
	gdanskBranchInformation
	gdanskBranchMixed
)

func attachedGdanskEntries(tree *vaadin.StateTree) ([]gdanskTreeEntry, error) {
	entries := make([]gdanskTreeEntry, 0)
	pending := []gdanskTreeVisit{}
	for _, root := range tree.Roots() {
		pending = append(pending, gdanskTreeVisit{node: root})
	}
	visited := map[vaadin.NodeID]bool{}
	for len(pending) > 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		if current.depth > gdanskSelectionMaxDepth {
			return nil, errors.New("content tree exceeds selection depth")
		}
		if visited[current.node.ID()] {
			return nil, errors.New("content tree contains repeated node")
		}
		if len(visited) >= gdanskSelectionMaxNodes {
			return nil, errors.New("content tree exceeds selection size")
		}
		visited[current.node.ID()] = true
		if vaadin.IsHidden(current.node) {
			continue
		}
		entries = append(entries, gdanskTreeEntry{
			node:   current.node,
			parent: current.parent,
			depth:  current.depth,
		})
		if vaadin.IsExcludedControl(current.node) {
			continue
		}
		children := tree.Children(current.node.ID())
		if len(pending)+len(children) > gdanskSelectionMaxNodes {
			return nil, errors.New("content tree exceeds selection size")
		}
		for index := len(children) - 1; index >= 0; index-- {
			pending = append(pending, gdanskTreeVisit{
				node:   children[index],
				parent: current.node.ID(),
				depth:  current.depth + 1,
			})
		}
	}
	return entries, nil
}

func lowestGdanskCommonAncestor(entries map[vaadin.NodeID]gdanskTreeEntry, leftID, rightID vaadin.NodeID) (vaadin.NodeID, bool) {
	left, leftExists := entries[leftID]
	right, rightExists := entries[rightID]
	if !leftExists || !rightExists {
		return 0, false
	}
	for left.depth > right.depth {
		leftID = left.parent
		left = entries[leftID]
	}
	for right.depth > left.depth {
		rightID = right.parent
		right = entries[rightID]
	}
	for leftID != rightID {
		if leftID == 0 || rightID == 0 {
			return 0, false
		}
		leftID = left.parent
		rightID = right.parent
		left = entries[leftID]
		right = entries[rightID]
	}
	return leftID, leftID != 0
}

func isGdanskDescendant(entries map[vaadin.NodeID]gdanskTreeEntry, nodeID, ancestorID vaadin.NodeID) bool {
	for nodeID != 0 {
		if nodeID == ancestorID {
			return true
		}
		entry, exists := entries[nodeID]
		if !exists {
			return false
		}
		nodeID = entry.parent
	}
	return false
}

func hasGdanskContainerClasses(classes []string) bool {
	wanted := map[string]bool{"container": false, "mt-md-3": false, "py-2": false, "flex-column": false}
	for _, className := range classes {
		if _, exists := wanted[className]; exists {
			wanted[className] = true
		}
	}
	for _, found := range wanted {
		if !found {
			return false
		}
	}
	return true
}

func classifyGdanskBranch(tree *vaadin.StateTree, root vaadin.Node) (gdanskBranchKind, error) {
	if vaadin.IsHidden(root) {
		return gdanskBranchExcluded, nil
	}
	containsReadOnly := false
	containsInteractive := false
	pending := []gdanskTreeVisit{{node: root}}
	visited := map[vaadin.NodeID]bool{}
	for len(pending) > 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		if current.depth > gdanskSelectionMaxDepth {
			return gdanskBranchExcluded, errors.New("content branch exceeds selection depth")
		}
		if visited[current.node.ID()] {
			return gdanskBranchExcluded, errors.New("content branch contains repeated node")
		}
		visited[current.node.ID()] = true
		if len(visited) > gdanskSelectionMaxNodes {
			return gdanskBranchExcluded, errors.New("content branch exceeds selection size")
		}
		if vaadin.IsHidden(current.node) {
			continue
		}
		if _, ok := vaadin.ReadOnlyValue(current.node); ok {
			containsReadOnly = true
		}
		if vaadin.IsExcludedControl(current.node) {
			containsInteractive = true
			continue
		}
		children := tree.Children(current.node.ID())
		if len(pending)+len(children) > gdanskSelectionMaxNodes {
			return gdanskBranchExcluded, errors.New("content branch exceeds selection size")
		}
		for index := len(children) - 1; index >= 0; index-- {
			pending = append(pending, gdanskTreeVisit{
				node:   children[index],
				parent: current.node.ID(),
				depth:  current.depth + 1,
			})
		}
	}
	if containsReadOnly && containsInteractive {
		return gdanskBranchMixed, nil
	}
	if containsReadOnly {
		return gdanskBranchInformation, nil
	}
	return gdanskBranchExcluded, nil
}
