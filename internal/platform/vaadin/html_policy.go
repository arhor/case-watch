package vaadin

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// IsHidden reports only the node's own explicit visibility exclusions. Callers
// must also honor ancestor exclusions; absent visibility does not mean hidden.
func IsHidden(node Node) bool {
	if visible, present := node.Visible(); present && !visible {
		return true
	}
	if _, present := node.Property(AttributesFeature, "hidden"); present {
		return true
	}
	hidden, _ := node.Property(AttributesFeature, "aria-hidden")
	if hidden == true || normalizedString(hidden) == "true" {
		return true
	}
	display := styleValue(node, "display")
	visibility := styleValue(node, "visibility")
	return display == "none" || visibility == "hidden" || visibility == "collapse"
}

// ReadOnlyValue preserves a supported field's explicitly empty string. Missing
// or non-string values and non-boolean readonly properties are unavailable.
func ReadOnlyValue(node Node) (string, bool) {
	if htmlTag(node) != "vaadin-text-field" && htmlTag(node) != "vaadin-text-area" {
		return "", false
	}
	readonly, _ := node.Property(PropertiesFeature, "readonly")
	if readonly != true {
		return "", false
	}
	value, _ := node.Property(PropertiesFeature, "value")
	text, ok := value.(string)
	return text, ok
}

// IsExcludedControl reports a node's own active/interactive subtree exclusion,
// including resource-loading elements. Ordinary anchors remain passive content.
// Hidden nodes are handled separately by IsHidden.
func IsExcludedControl(node Node) bool {
	switch htmlTag(node) {
	case "vaadin-text-field", "vaadin-text-area":
		_, ok := ReadOnlyValue(node)
		return !ok
	case "script", "style", "template", "iframe", "object", "embed", "svg", "math", "link", "meta", "img", "audio", "video", "source", "track", "picture", "base", "canvas", "noscript",
		"form", "input", "button", "select", "textarea", "option", "optgroup", "fieldset", "datalist", "output", "details", "summary",
		"vaadin-password-field", "vaadin-button", "vaadin-checkbox", "vaadin-checkbox-group", "vaadin-radio-button", "vaadin-radio-group", "vaadin-select", "vaadin-combo-box", "vaadin-multi-select-combo-box", "vaadin-date-picker", "vaadin-time-picker", "vaadin-date-time-picker", "vaadin-number-field", "vaadin-integer-field", "vaadin-email-field", "vaadin-upload", "vaadin-rich-text-editor", "vaadin-login-form", "vaadin-login-overlay", "vaadin-context-menu", "vaadin-menu-bar":
		return true
	}
	for _, feature := range []FeatureID{AttributesFeature, PropertiesFeature} {
		value, _ := node.Property(feature, "contenteditable")
		if value == true || normalizedString(value) == "true" || normalizedString(value) == "plaintext-only" {
			return true
		}
		if feature == AttributesFeature && value == "" {
			return true
		}
	}
	return false
}

// -- Internals ----------------------------------------------------------------

func normalizedString(value any) string {
	text, _ := value.(string)
	return strings.ToLower(strings.TrimSpace(text))
}

func styleValue(node Node, key string) string {
	value, _ := node.Property(StylesFeature, key)
	return normalizedString(value)
}

func emphasis(node Node) (bool, bool) {
	weight := styleValue(node, "font-weight")
	number, err := strconv.Atoi(weight)
	bold := htmlTag(node) == "strong" || htmlTag(node) == "b" || weight == "bold" || (err == nil && number >= 600 && number <= 900)
	style := styleValue(node, "font-style")
	italic := htmlTag(node) == "em" || htmlTag(node) == "i" || style == "italic" || style == "oblique"
	return bold, italic
}

func safeHref(node Node) string {
	value, _ := node.Property(AttributesFeature, "href")
	href, ok := value.(string)
	if !ok || strings.ContainsFunc(href, func(character rune) bool { return unicode.IsSpace(character) || unicode.IsControl(character) }) {
		return ""
	}
	parsed, err := url.Parse(href)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" {
		return ""
	}
	return href
}

func htmlTag(node Node) string { return strings.ToLower(node.Tag()) }
