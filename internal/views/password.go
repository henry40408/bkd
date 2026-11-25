package views

func passwordField(label, name, autocomplete string, minLength bool) node {
	attrs := []attr{a("type", "password"), a("name", name), a("autocomplete", autocomplete),
		when(minLength, a("minlength", "12")), flag("required")}
	return labelled(label, void("input", attrs...))
}

func passwordSection(view SettingsView) node {
	return sheetSection("Change password",
		el("p", []attr{classes("hint")},
			text("At least 12 characters, without your username. Changing it signs you out everywhere else.")),
		el("form", []attr{classes("stack"), a("method", "post"), a("action", "/settings/password")},
			passwordField("Current password ", "current", "current-password", false),
			passwordField("New password ", "new", "new-password", true),
			passwordField("Repeat new password ", "again", "new-password", false),
			errorLine(view.PasswordError),
			formButtons(iconSubmitButton(iconKey, "Change password")),
		),
	)
}
