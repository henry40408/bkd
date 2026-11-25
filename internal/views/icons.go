package views

// iconName names one of the 16x16 line icons. A constant per icon makes a
// misspelt name a compile error instead of an empty drawing.
type iconName string

const (
	iconOK         iconName = "ok"
	iconBroken     iconName = "broken"
	iconRedirected iconName = "redirected"
	iconUnknown    iconName = "unknown"
	iconEdit       iconName = "edit"
	iconTrash      iconName = "trash"
	iconPlus       iconName = "plus"
	iconShuffle    iconName = "shuffle"
	iconSelect     iconName = "select"
	iconAll        iconName = "all"
	iconTagAdd     iconName = "tagAdd"
	iconTagRemove  iconName = "tagRemove"
	iconLink       iconName = "link"
	iconPage       iconName = "page"
	iconSearch     iconName = "search"
	iconMenu       iconName = "menu"
	iconUpload     iconName = "upload"
	iconDownload   iconName = "download"
	iconSignOut    iconName = "signout"
	iconSignIn     iconName = "signin"
	iconKey        iconName = "key"
	iconUserAdd    iconName = "userAdd"
	iconBan        iconName = "ban"
	iconCopy       iconName = "copy"
)

// iconPaths is the stroke path of each icon, drawn on a 16x16 grid.
var iconPaths = map[iconName]string{
	iconOK:         "M3 8.5l3.2 3.2L13 4.5",
	iconBroken:     "M4 4l8 8M12 4l-8 8",
	iconRedirected: "M2.5 8h10.5M9 4l4 4-4 4",
	iconUnknown:    "M5.6 6a2.4 2.4 0 1 1 3.6 2.1c-.8.5-1.2 1-1.2 1.9M8 12.6v.2",
	iconEdit:       "M3 13l.6-2.9 7.3-7.3 2.3 2.3-7.3 7.3zM9.5 4.2l2.3 2.3",
	iconTrash:      "M3 4.5h10M6.5 4.5V3h3v1.5M4.5 4.5l.6 8.5h5.8l.6-8.5M6.8 7v3.5M9.2 7v3.5",
	iconPlus:       "M8 3v10M3 8h10",
	iconShuffle:    "M2 5h2c3 0 4.5 6 7.5 6h2M2 11h2c1 0 1.8-.7 2.5-1.6M9 6.6c.7-.9 1.5-1.6 2.5-1.6h2M11.8 3.2L13.6 5l-1.8 1.8M11.8 9.2l1.8 1.8-1.8 1.8",
	iconSelect:     "M3 3h10v10H3zM5.5 8l2 2 3.2-3.6",
	iconAll:        "M1.5 8.5l2.8 2.8L10 4.8M7.8 11.3l6.2-6.5",
	iconTagAdd:     "M2 3h4.5l5.5 5.5-4.5 4.5L2 7.5zM4.6 5.6v.1M12.5 1.5v4M10.5 3.5h4",
	iconTagRemove:  "M2 3h4.5l5.5 5.5-4.5 4.5L2 7.5zM4.6 5.6v.1M10.5 3.5h4",
	iconPage:       "M4 2h5l3 3v9H4zM9 2v3h3M6 8h4M6 10.5h4",
	iconLink:       "M6.8 9.2a2.6 2.6 0 0 0 3.7 0l2-2a2.6 2.6 0 0 0-3.7-3.7l-.8.8M9.2 6.8a2.6 2.6 0 0 0-3.7 0l-2 2a2.6 2.6 0 0 0 3.7 3.7l.8-.8",
	iconSearch:     "M7 2.5a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9zM10.4 10.4l3.1 3.1",
	iconMenu:       "M2.5 4h11M2.5 8h11M2.5 12h11",
	iconUpload:     "M8 10.5V3M5 5.5l3-3 3 3M3 10.5v2.5h10v-2.5",
	iconDownload:   "M8 2.5V10M5 7.5l3 3 3-3M3 10.5v2.5h10v-2.5",
	iconSignOut:    "M6.5 2.5H3v11h3.5M10 5l3 3-3 3M13 8H6.5",
	iconSignIn:     "M9.5 2.5H13v11H9.5M6.5 5l3 3-3 3M9.5 8h-7",
	iconKey:        "M5.5 13a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zM7.3 8.7L13 3M11 5l1.5 1.5M9.3 6.7l1.2 1.2",
	iconUserAdd:    "M6.5 8a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zM2 13.5C2 11.3 4 10 6.5 10s4.5 1.3 4.5 3.5M12.5 4.5v4M10.5 6.5h4",
	iconBan:        "M8 2.5a5.5 5.5 0 1 0 0 11 5.5 5.5 0 0 0 0-11zM4.1 4.1l7.8 7.8",
	iconCopy:       "M5.5 5.5h7.5v8H5.5zM3 10.5V2.5h7.5",
}

func icon(name iconName) node {
	return el("svg", []attr{a("viewBox", "0 0 16 16"), a("aria-hidden", "true")},
		el("path", []attr{a("d", iconPaths[name])}))
}

// iconLabel is an icon with its text. A button with the class "ico" shows
// only the icon on a narrow screen; the text stays for screen readers.
func iconLabel(name iconName, label string) node {
	return group(icon(name), el("span", []attr{classes("lbl")}, text(label)))
}
