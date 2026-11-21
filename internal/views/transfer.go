package views

// InvalidExample is one line of an import that was not a web link.
type InvalidExample struct {
	URL    string
	Reason string
}

// ImportReport is what an import did.
type ImportReport struct {
	Added      int64
	Duplicates int64
	Invalid    int64
	Examples   []InvalidExample
}

// TransferView is the import and export page.
type TransferView struct {
	Shell  Shell
	Error  string
	Report *ImportReport
}

func importResult(report ImportReport) node {
	summary := group(
		text("Added "), number(report.Added),
		text(", skipped "), number(report.Duplicates), text(" you already had"),
		maybe(report.Invalid > 0, func() node {
			return group(text(", and ignored "), number(report.Invalid), text(" that are not web links"))
		}),
		text("."),
	)
	examples := make([]node, len(report.Examples))
	for i, example := range report.Examples {
		examples[i] = el("li", nil, text(example.URL+" ("+example.Reason+")"))
	}
	return el("div", []attr{classes("import-result"), a("role", "status")},
		el("p", nil, summary),
		maybe(len(examples) > 0, func() node { return el("ul", nil, examples...) }),
	)
}

func importForm(view TransferView) node {
	return el("form", []attr{classes("drop"), a("method", "post"), a("action", "/transfer/import"),
		a("enctype", "multipart/form-data")},
		field("Bookmarks file ", void("input", a("type", "file"), a("name", "file"),
			a("accept", ".html,.htm,text/html"), flag("required"))),
		el("button", []attr{classes("btn", "primary", "ico"), a("type", "submit"), a("title", "Import bookmarks")},
			iconLabel(iconUpload, "Import bookmarks")),
		// Dropping on the box needs the script, which shows this line.
		el("p", []attr{classes("hint"), flag("data-script-only"), flag("hidden")}, text("Or drop the file in this box.")),
		errorLine(view.Error),
	)
}

// downloadLink keeps its text on a narrow screen: the two formats share one icon.
func downloadLink(format, label string) node {
	return el("a", []attr{classes("btn"), a("href", "/api/export?format="+format), flag("download")},
		iconLabel(iconDownload, label))
}

// TransferPage renders the import and export page.
func TransferPage(view TransferView) string {
	result := maybe(view.Report != nil, func() node { return importResult(*view.Report) })
	content := group(
		el("h1", nil, text("Import and export")),
		sheet(
			sheetSection("Import",
				el("p", nil,
					text("Choose a bookmarks file exported from Chrome, Firefox, Safari, Pocket or Raindrop "+
						"(an .html file). Folders become tags. A link you already saved is skipped.")),
				importForm(view),
				result,
			),
			sheetSection("Export",
				el("p", nil,
					text("Download all of your bookmarks. Use HTML to move them to a browser or another service, "+
						"and JSON as a plain backup.")),
				formButtons(downloadLink("html", "Download HTML"), downloadLink("json", "Download JSON")),
			),
		),
	)
	return framed("Import and export · bkd", view.Shell, content)
}
