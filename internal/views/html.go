package views

import (
	"strconv"
	"strings"
)

// node is markup that is already safe to concatenate: every piece of text and
// every attribute value in it went through escape.
type node string

// attr is one attribute. A flag renders bare (`defer`), like maud does. The
// zero attr renders nothing, which lets when() drop an attribute.
type attr struct {
	name, value string
	flag        bool
}

func a(name, value string) attr { return attr{name: name, value: value} }

func flag(name string) attr { return attr{name: name, flag: true} }

// when keeps at only if ok is true, as maud's `name[ok]` does.
func when(ok bool, at attr) attr {
	if !ok {
		return attr{}
	}
	return at
}

// classes is the class attribute. maud writes class (and then id) before the
// other attributes, whatever order the source lists them in, so callers put
// classes first and id second.
func classes(names ...string) attr { return a("class", strings.Join(names, " ")) }

// escape covers both contexts the views use, element text and double-quoted
// attribute values. maud escapes exactly these four characters in both, and
// html/template would write `&#34;` instead of `&quot;`.
var escape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace

// text escapes s for use as element content.
func text(s string) node { return node(escape(s)) }

// number writes n as element content.
func number(n int64) node { return node(strconv.FormatInt(n, 10)) }

// group concatenates nodes without any whitespace between them.
func group(children ...node) node {
	var b strings.Builder
	for _, child := range children {
		b.WriteString(string(child))
	}
	return node(b.String())
}

func openTag(tag string, attrs []attr) string {
	var b strings.Builder
	b.WriteString("<" + tag)
	for _, at := range attrs {
		if at.name == "" {
			continue
		}
		b.WriteString(" " + at.name)
		if !at.flag {
			b.WriteString(`="` + escape(at.value) + `"`)
		}
	}
	b.WriteString(">")
	return b.String()
}

// el renders an element with children and a closing tag.
func el(tag string, attrs []attr, children ...node) node {
	return node(openTag(tag, attrs) + string(group(children...)) + "</" + tag + ">")
}

// void renders an element that has no content and no closing tag.
func void(tag string, attrs ...attr) node {
	return node(openTag(tag, attrs))
}

// maybe renders child only when ok is true.
func maybe(ok bool, child func() node) node {
	if !ok {
		return ""
	}
	return child()
}
