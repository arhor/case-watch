package clients

import (
	"errors"
	"strings"

	"github.com/arhor/case-watch/internal/platform/vaadin"
)

// -- Internals ----------------------------------------------------------------

type gdanskTreeEntry struct {
	node      vaadin.Node
	ancestors []vaadin.NodeID
}

func selectGdanskCaseContent(tree *vaadin.StateTree, stage string) ([]vaadin.Node, error) {
	entries := attachedGdanskEntries(tree)
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
	anchors := map[vaadin.NodeID]bool{}
	for _, captionID := range captions {
		for _, fieldID := range stageFields {
			if common, ok := lowestGdanskCommonAncestor(entriesByID[captionID], entriesByID[fieldID]); ok {
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
	anchorEntry := entriesByID[anchor]
	var containers []vaadin.NodeID
	for _, id := range append(anchorEntry.ancestors, anchor) {
		entry := entriesByID[id]
		if entry.node.Tag() == "div" && hasGdanskContainerClasses(entry.node.Classes()) {
			containers = append(containers, id)
		}
	}
	if len(containers) != 1 {
		return nil, errors.New("missing or ambiguous content container")
	}
	container := entriesByID[containers[0]].node
	var roots []vaadin.Node
	for _, branch := range tree.Children(container.ID()) {
		classification := classifyGdanskBranch(tree, branch)
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
		for _, descendant := range tree.Subtree(root.ID()) {
			if descendant.ID() == anchor {
				stageIncluded = true
			}
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

func attachedGdanskEntries(tree *vaadin.StateTree) []gdanskTreeEntry {
	entries := []gdanskTreeEntry{}
	var visit func(vaadin.Node, []vaadin.NodeID)
	visit = func(node vaadin.Node, ancestors []vaadin.NodeID) {
		if vaadin.IsHidden(node) {
			return
		}
		entries = append(entries, gdanskTreeEntry{node: node, ancestors: append([]vaadin.NodeID(nil), ancestors...)})
		nextAncestors := append(append([]vaadin.NodeID(nil), ancestors...), node.ID())
		if vaadin.IsExcludedControl(node) {
			return
		}
		for _, child := range tree.Children(node.ID()) {
			visit(child, nextAncestors)
		}
	}
	for _, root := range tree.Roots() {
		visit(root, nil)
	}
	return entries
}

func lowestGdanskCommonAncestor(left, right gdanskTreeEntry) (vaadin.NodeID, bool) {
	leftPath := append(append([]vaadin.NodeID(nil), left.ancestors...), left.node.ID())
	rightPath := append(append([]vaadin.NodeID(nil), right.ancestors...), right.node.ID())
	limit := len(leftPath)
	if len(rightPath) < limit {
		limit = len(rightPath)
	}
	var common vaadin.NodeID
	for index := 0; index < limit && leftPath[index] == rightPath[index]; index++ {
		common = leftPath[index]
	}
	return common, common != 0
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

func classifyGdanskBranch(tree *vaadin.StateTree, root vaadin.Node) gdanskBranchKind {
	if vaadin.IsHidden(root) {
		return gdanskBranchExcluded
	}
	containsReadOnly := false
	containsInteractive := false
	var inspect func(vaadin.Node)
	inspect = func(node vaadin.Node) {
		if vaadin.IsHidden(node) {
			return
		}
		if _, ok := vaadin.ReadOnlyValue(node); ok {
			containsReadOnly = true
		}
		if vaadin.IsExcludedControl(node) {
			containsInteractive = true
			return
		}
		for _, child := range tree.Children(node.ID()) {
			inspect(child)
		}
	}
	inspect(root)
	if containsReadOnly && containsInteractive {
		return gdanskBranchMixed
	}
	if containsReadOnly {
		return gdanskBranchInformation
	}
	return gdanskBranchExcluded
}
