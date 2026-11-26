// Package vaadin reconstructs the observed subset of a Vaadin UI state tree.
// It neither executes server instructions nor owns HTTP, authentication,
// synchronization counters, security keys, or portal-specific content selection.
//
// DecodeBootstrap extracts the uidl and config JSON objects. DecodeResponse
// accepts the optional for(;;); framing and returns messages in wire order.
// Call Apply once per complete message, then commit that message's counters and
// optional security key in the session. Absent keys mean retain the previous key.
// A failure must stop that session's request sequence. Errors contain fixed
// diagnostics only, never remote bodies, property values or security metadata.
//
// Supported features are element metadata (0), component properties (1), ordered
// child references (2), attributes (3), text (7), ordered classes (11) and styles
// (12). Map features support put/remove. Map values retain their JSON types,
// including false, null and empty strings; nodeValue is a typed NodeReference,
// distinct from a scalar number in value. Styles are retained without CSS
// interpretation. Child splices use addNodes; class splices use string-valued
// add. Both lists support clear, which clears only the addressed list. Omitted
// remove means zero and omitted additions mean deletion only.
//
// Listener and push feature mutations (4, 5, 6) with known operation names are
// intentionally ignored. Feature 19 accepts only empty with a boolean featType:
// this declares an empty non-rendering feature, never clears existing children.
// Other feature IDs, unknown operation names and unsupported operations on
// supported features are rejected. Empty declarations for other features are
// outside this observed subset. Server execute instructions and unrelated
// envelope fields are ignored. This is not a full Vaadin runtime.
//
// Each tree belongs to one UI/session. Attach and other mutation targets register
// nodes, permitting the implicit body root and forward references. Detach removes
// only that node; unlinking a child does not destroy it or its descendants.
// References must resolve at the end of each message. Child cycles, duplicate
// parents and dangling child/property references are rejected across the whole
// registry, including disconnected nodes. Apply is transactional at the message
// boundary, so failed applications leave previous state and node handles intact.
//
// Nodes returns all registered nodes, including inert non-DOM protocol nodes.
// Roots returns parentless DOM nodes; a portal's content container is not assumed
// to be a document root. Independent roots are sorted by ID; Children and Subtree
// preserve protocol child order. Subtree traversal is iterative and bounded by
// the registry. Accessors copy mutable values; node handles retain identity while
// registered. They are value handles to private state, so replacing a handle
// cannot mutate the registry. Detach ends that registration: a reused numeric ID
// receives a new handle and removed handles retain their last committed values.
// The tree supports a single caller and needs no shared registry.
package vaadin
