package views

const titlePlaceholder = "Leave blank to use the domain"

// SiteDetails is what the page at URL says about itself. The add and edit forms
// show it as the placeholders of the title and the note, and send it back in
// hidden fields, so that a title or a note left blank is saved as it. URL says
// which address the details belong to: the server drops them when it differs
// from the address saved.
type SiteDetails struct {
	URL         string
	Title       string
	Description string
}

// titleField is the title input. Its placeholder is the page's title when there
// is one; the script puts the usual one back when the address changes.
func titleField(value string, site SiteDetails) node {
	placeholder := titlePlaceholder
	if site.Title != "" {
		placeholder = site.Title
	}
	return field("Title ", void("input", a("type", "text"), a("name", "title"), a("value", value),
		a("placeholder", placeholder), a("data-default-placeholder", titlePlaceholder)))
}

// noteField is the note input. Its placeholder is the page's description.
func noteField(value string, site SiteDetails, rows string) node {
	return field("Note ", el("textarea", []attr{a("name", "note"), a("rows", rows), when(site.Description != "", a("placeholder", site.Description))}, text(value)))
}

// siteFields carries the page's details with the form, and holds the line where
// the script says how reading the page went.
func siteFields(site SiteDetails) node {
	return group(
		hidden("site_url", site.URL),
		hidden("site_title", site.Title),
		hidden("site_description", site.Description),
		el("p", []attr{classes("hint"), a("role", "status"), a("data-site-status", ""), flag("hidden")}),
	)
}
