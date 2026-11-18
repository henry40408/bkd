package netscape

import (
	"strconv"
	"strings"
)

// Link is one `<A>` link, with the folders around it already turned into tags.
type Link struct {
	URL   string
	Title string
	Note  string
	Tags  []string
	// Added is the ADD_DATE attribute as written, normally Unix seconds. Nil
	// when it is missing or not a number.
	Added *int64
}

// Parse reads every link out of a bookmark file. It never fails: whatever
// cannot be understood is skipped.
func Parse(html string) []Link {
	parser := parser{}
	for _, tok := range tokenize(html) {
		parser.handle(tok)
	}
	parser.flushNote()
	return parser.links
}

type capture int

const (
	captureNothing capture = iota
	captureFolder
	captureLink
	captureNote
)

// folder is the name of the folder a `<DL>` belongs to. A `<DL>` can open
// without one.
type folder struct {
	name  string
	named bool
}

type parser struct {
	links []Link
	// folders has one entry per open `<DL>`.
	folders       []folder
	pendingFolder folder
	openLink      []attribute
	linkIsOpen    bool
	// noteTarget is where `<DD>` text goes: the link just closed, until
	// another item starts.
	noteTarget    int
	hasNoteTarget bool
	capture       capture
	text          strings.Builder
}

func (p *parser) handle(tok token) {
	switch {
	case tok.kind == textToken:
		if p.capture != captureNothing {
			p.text.WriteString(tok.text)
		}
	case tok.kind == startTag && tok.name == "br" && p.capture == captureNote:
		p.text.WriteByte('\n')
	default:
		p.flushNote()
		p.handleTag(tok)
	}
}

func (p *parser) handleTag(tok token) {
	if tok.kind == startTag {
		p.handleStart(tok)
		return
	}
	switch tok.name {
	case "h3":
		p.pendingFolder = folder{name: collapseWhitespace(p.takeText()), named: true}
	case "dl":
		if len(p.folders) > 0 {
			p.folders = p.folders[:len(p.folders)-1]
		}
	case "a":
		p.finishLink()
	}
}

func (p *parser) handleStart(tok token) {
	switch {
	case tok.name == "h3":
		p.hasNoteTarget = false
		p.startCapture(captureFolder)
	case tok.name == "dl":
		p.hasNoteTarget = false
		p.folders = append(p.folders, p.pendingFolder)
		p.pendingFolder = folder{}
	case tok.name == "a":
		p.openLink, p.linkIsOpen = tok.attributes, true
		p.startCapture(captureLink)
	case tok.name == "dd" && p.hasNoteTarget:
		p.startCapture(captureNote)
	}
}

func (p *parser) startCapture(next capture) {
	p.capture = next
	p.text.Reset()
}

func (p *parser) takeText() string {
	p.capture = captureNothing
	text := p.text.String()
	p.text.Reset()
	return text
}

func (p *parser) finishLink() {
	title := collapseWhitespace(p.takeText())
	if !p.linkIsOpen {
		return
	}
	attributes := p.openLink
	p.openLink, p.linkIsOpen = nil, false

	tags := []string{}
	for _, open := range p.folders {
		if open.named {
			tags = append(tags, open.name)
		}
	}
	href, _ := findAttribute(attributes, "href")
	tagList, _ := findAttribute(attributes, "tags")
	for _, tag := range strings.Split(tagList, ",") {
		if strings.TrimSpace(tag) != "" {
			tags = append(tags, tag)
		}
	}
	p.links = append(p.links, Link{
		URL:   strings.TrimSpace(href),
		Title: title,
		Tags:  tags,
		Added: addedDate(attributes),
	})
	p.noteTarget, p.hasNoteTarget = len(p.links)-1, true
}

// addedDate prefers ADD_DATE and falls back to Pocket's time_added only when
// ADD_DATE is absent, not when it is unreadable.
func addedDate(attributes []attribute) *int64 {
	value, found := findAttribute(attributes, "add_date")
	if !found {
		value, found = findAttribute(attributes, "time_added")
	}
	if !found {
		return nil
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return nil
	}
	return &seconds
}

func findAttribute(attributes []attribute, wanted string) (string, bool) {
	for _, attribute := range attributes {
		if attribute.name == wanted {
			return attribute.value, true
		}
	}
	return "", false
}

func (p *parser) flushNote() {
	if p.capture != captureNote {
		return
	}
	note := p.takeText()
	if p.hasNoteTarget && p.noteTarget < len(p.links) {
		p.links[p.noteTarget].Note = strings.TrimSpace(note)
	}
}

func collapseWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
