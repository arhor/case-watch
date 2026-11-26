# Static HTML rendering

`RenderHTML(tree, roots...)` renders ordered `Node` handles from the same
`StateTree` in one invocation. The selected roots act as a virtual container:
selecting adjacent rows separately produces the same table as selecting their
neutral grouping parent. Selection, source order and exclusion of ancestors are
the caller's responsibility. The mapper never searches for portal fields or
walks unrelated registered nodes, and never modifies the tree.

An empty selection or valid content with nothing renderable returns an empty
fragment and nil error. A nil tree with selected roots, zero/foreign/stale node
handle, duplicate/overlapping selection, dangling reference or cycle returns a
fixed diagnostic and no partial output. A selected subtree deeper than 512
edges is rejected before recursive rendering. Validation includes hidden and
excluded descendants. Tree mutation must not run concurrently with rendering.

## Components and safety

Text is escaped once, preserving Unicode and whitespace. Labels and spans are
transparent inline containers. Explicit `strong`/`b` and `em`/`i` become semantic
emphasis, as do feature-12 `font-weight: bold` or numeric 600–900 and
`font-style: italic`/`oblique`. Repeated emphasis is suppressed. Styles and
classes never pass through to output.

Read-only `vaadin-text-field` and `vaadin-text-area` require boolean `readonly:
true` and a string `value` in feature 1. An empty string remains an available
value. Missing/non-string values are unavailable, with no placeholder/label
fallback. Textarea CRLF/CR normalize to LF; each newline becomes `br`.

`div`, `section`, `article` and Vaadin vertical/horizontal layouts group ordered
block content using `div` or transparent wrappers. Supported standard elements
are `p`, `h1`–`h6`, `ul`, `ol`, `li`, `strong`, `b`, `em`, `i`, `code`, `pre`, `br`,
`hr` and `a`. Invalid source nesting uses transparent wrappers; orphan list
items use `div`, lists with non-item content are transparent, and emphasis wraps
inline content inside blocks. Nested links never generate nested anchors.

Links require an absolute HTTP(S) URL with a nonempty hostname, no userinfo,
whitespace or control characters. Unsafe/relative links retain readable child
content. Only the escaped, quoted `href` is emitted. URLs are never fetched.
All output tag names are fixed; arbitrary source tags and attributes, raw HTML,
styles, handlers, IDs and resource URLs are never forwarded.

`IsHidden` checks only a node's own explicit metadata `visible: false`, presence
of `hidden` (even `hidden="false"`), true `aria-hidden`, or normalized feature-12
`display: none` / `visibility: hidden` / `visibility: collapse`. No computed CSS,
class-based visibility or inherited styles are inferred. Rendering stops at
hidden ancestors. `ReadOnlyValue` and `IsExcludedControl` likewise describe the
node itself and are reusable by content selectors.

Editable native controls/forms, known interactive Vaadin controls, passwords,
contenteditable containers, and active/resource elements (including script,
style, template, iframe, SVG, MathML, image, media and embeds) exclude their
entire subtrees. Unknown passive components transparently preserve supported
children. `html_policy.go` defines the finite exclusion set. There is no grid
data-provider reconstruction, browser/Shadow DOM emulation or JavaScript.

## Structural tables and fallback

A row is a `div.flex-md-row` with exactly two visible `div` column children,
ignoring hidden subtrees and whitespace-only text. The columns must pair
`col-md-4` / `col-md-8` or `col-lg-3` / `col-lg-9`. Either breakpoint suffices;
class order and unrelated tokens do not matter. Horizontal layout alone never
qualifies.

The left column must contain exactly one visible label, optionally nested in
neutral wrappers, with no unrelated text. The label may be empty. The right
column must contain at least one available read-only value, possibly deeply
nested; safe additional text, formatting, values and separators remain in
order. Visible excluded controls/active elements or nested candidate rows in
either column prevent matching. Captions, languages, values and node IDs play
no role in recognition.

Adjacent rows become one `table` / `tbody` with exactly two `td` cells per `tr`.
Captions are bold when nonempty; empty captions/values retain their own cells.
Row-only neutral `div` and `vaadin-vertical-layout` groups flatten recursively.
Empty neutral wrappers and block whitespace do not split runs. A wrapper with
standalone text, emphasis, links, headings, non-row content or interactive
boundaries remains in place. Meaningful non-row items flush the pending table;
rows are never searched globally or moved across boundaries.

Unrecognized layouts use the ordinary safe renderer, preserving readable
content in source order. No domain interpretation, invented captions, headers,
status messages, business grouping or destination-specific markup is added.
The shared fixture golden is documented in the fixture directory's README.
