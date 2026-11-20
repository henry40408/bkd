package netscape

import "testing"

func text(value string) token { return token{kind: textToken, text: value} }

func start(name string, attributes ...attribute) token {
	return token{kind: startTag, name: name, attributes: attributes}
}

func end(name string) token { return token{kind: endTag, name: name} }

func TestTokenizeProducesExactlyTheseTokens(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []token
	}{
		{"no markup", "plain", []token{text("plain")}},
		{"markup first has no empty text before it", "<a>", []token{start("a")}},
		{"text around a tag", "x<b>y", []token{text("x"), start("b"), text("y")}},
		{"comment is dropped", "<!-- c -->x", []token{text("x")}},
		{"empty comment is dropped", "<!---->x", []token{text("x")}},
		{"unterminated comment eats the rest", "a<!-- b", []token{text("a")}},
		{"bare comment opener", "<!--", nil},
		{"doctype is dropped", "<!DOCTYPE html>x", []token{text("x")}},
		{"processing instruction is dropped", "<?xml?>x", []token{text("x")}},
		{"unterminated doctype eats the rest", "<!DOCTYPE x", nil},
		{"lone angle bracket is text", "<", []token{text("<")}},
		{"angle bracket before a space is text", "< a>", []token{text("<"), text(" a>")}},
		{"tag name boundaries", "<a><z><A><Z>", []token{start("a"), start("z"), start("a"), start("z")}},
		{"characters next to the letters are not tags", "<`<{<@<[", []token{
			text("<"), text("`"), text("<"), text("{"), text("<"), text("@"), text("<"), text("["),
		}},
		{"end tag", "</A >", []token{end("a")}},
		{"unterminated tag", "<a", []token{start("a")}},
		{"unterminated tag with attribute", "<a x=1", []token{start("a", attribute{"x", "1"})}},
		{"attribute without a name", "<a =x>", []token{start("a", attribute{"", "x"})}},
		{"empty quoted value then another attribute", `<a x="" y=1>`, []token{start("a", attribute{"x", ""}, attribute{"y", "1"})}},
		{"quoted value then another attribute", `<a x="1" y='2' z>`, []token{start("a", attribute{"x", "1"}, attribute{"y", "2"}, attribute{"z", ""})}},
		{"unterminated quote takes the rest", `<a x="1`, []token{start("a", attribute{"x", "1"})}},
		{"self closing slash", "<br/>", []token{start("br")}},
		{"entity at the scan limit stays literal", "&#x0000000041;", []token{text("&#x0000000041;")}},
		{"entity just inside the scan limit", "&#x00000041;", []token{text("A")}},
		{"largest code point", "&#x10FFFF;", []token{text("\U0010FFFF")}},
		{"one past the largest code point", "&#x110000;", []token{text("&#x110000;")}},
		{"code that would wrap into a valid rune", "&#4294967361;", []token{text("&#4294967361;")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertEqual(t, tokenize(c.input), c.want)
		})
	}
}
