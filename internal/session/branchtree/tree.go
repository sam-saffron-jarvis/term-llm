// Package branchtree defines and orders the persisted conversation fork tree.
package branchtree

import (
	"sort"
	"time"
)

// Node is one normal session in a connected conversation tree.
type Node struct {
	SessionID             string    `json:"session_id"`
	SessionNumber         int64     `json:"session_number,omitempty"`
	ParentSessionID       string    `json:"parent_session_id,omitempty"`
	ForkAfterMessageID    int64     `json:"fork_after_message_id,omitempty"`
	ForkAfterSequence     int       `json:"fork_after_sequence"`
	CopiedAnchorMessageID int64     `json:"copied_anchor_message_id,omitempty"`
	Title                 string    `json:"title,omitempty"`
	AnchorRole            string    `json:"anchor_role,omitempty"`
	AnchorPreview         string    `json:"anchor_preview,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
}

// Tree is a connected component rooted at its oldest surviving ancestor.
// PathCount counts independently resumable linear sessions in the component;
// it intentionally equals len(Nodes), including non-leaf ancestor sessions.
type Tree struct {
	RootSessionID   string `json:"root_session_id"`
	ActiveSessionID string `json:"active_session_id"`
	PathCount       int    `json:"path_count"`
	Nodes           []Node `json:"nodes"`
}

// Order builds a stable, parent-first projection from the connected nodes.
func Order(sessionID string, byID map[string]Node) Tree {
	children := make(map[string][]Node)
	roots := make([]Node, 0, 1)
	for _, node := range byID {
		if _, parentSurvives := byID[node.ParentSessionID]; node.ParentSessionID == "" || !parentSurvives {
			roots = append(roots, node)
		} else {
			children[node.ParentSessionID] = append(children[node.ParentSessionID], node)
		}
	}
	less := func(a, b Node) bool {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.SessionNumber < b.SessionNumber
		}
		return a.CreatedAt.Before(b.CreatedAt)
	}
	sort.Slice(roots, func(i, j int) bool { return less(roots[i], roots[j]) })
	for parent := range children {
		sort.Slice(children[parent], func(i, j int) bool { return less(children[parent][i], children[parent][j]) })
	}

	tree := Tree{ActiveSessionID: sessionID}
	visited := make(map[string]bool, len(byID))
	var appendTree func(Node)
	appendTree = func(node Node) {
		if visited[node.SessionID] {
			return
		}
		visited[node.SessionID] = true
		tree.Nodes = append(tree.Nodes, node)
		for _, child := range children[node.SessionID] {
			appendTree(child)
		}
	}
	for _, root := range roots {
		if tree.RootSessionID == "" {
			tree.RootSessionID = root.SessionID
		}
		appendTree(root)
	}
	tree.PathCount = len(tree.Nodes)
	return tree
}
