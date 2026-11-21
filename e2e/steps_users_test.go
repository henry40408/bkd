package e2e

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/henry40408/bkd/e2e/internal/harness"
)

const (
	newUserForm = "form[action='/users']"
	userList    = ".people"
)

func registerUserSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the member `+quoted+` exists$`, withString(w.memberExists))
	sc.Step(`^the member `+quoted+` exists and is signed in elsewhere$`, withString(w.memberSignedInElsewhere))
	sc.Step(`^I open Manage users$`, w.openManageUsers)
	sc.Step(`^I create the user `+quoted+` with a temporary password$`, withString(w.createUser))
	sc.Step(`^I disable `+quoted+`$`, withString(func(username string) error {
		return w.click(fmt.Sprintf("button[aria-label='Disable %s']", username))
	}))
	sc.Step(`^I start resetting the password of `+quoted+`$`, withString(w.startResettingPassword))
	sc.Step(`^I reset the password of `+quoted+` to `+quoted+`$`, withTwoStrings(w.resetPassword))
	sc.Step(`^I start deleting the user `+quoted+`$`, withString(w.startDeletingUser))
	sc.Step(`^I delete `+quoted+`$`, withString(w.deleteUser))
	sc.Step(`^the user list shows `+quoted+`$`, withString(func(username string) error {
		return w.textContains(userList, username)
	}))
	sc.Step(`^the user list does not show `+quoted+`$`, withString(func(username string) error {
		return w.textLacks(userList, username)
	}))
	sc.Step(`^the user list shows `+quoted+` as disabled$`, withString(w.userListShowsDisabled))
	sc.Step(`^`+quoted+` can sign in with the temporary password$`, withString(func(username string) error {
		_, err := w.api.Login(username, harness.Password)
		return err
	}))
	sc.Step(`^`+quoted+` can sign in with the password `+quoted+`$`, withTwoStrings(func(username, password string) error {
		_, err := w.api.Login(username, password)
		return err
	}))
	sc.Step(`^the session of `+quoted+` no longer works$`, withString(w.sessionNoLongerWorks))
	sc.Step(`^I cannot open Manage users$`, w.cannotOpenManageUsers)
	sc.Step(`^the row of `+quoted+` has no Demote, Disable, Delete or Reset password control$`, withString(w.rowHasNoControls))
}

func (w *world) memberExists(username string) error {
	admin, err := w.mySession()
	if err != nil {
		return err
	}
	return w.api.CreateUser(admin, username, harness.Password, false)
}

func (w *world) memberSignedInElsewhere(username string) error {
	if err := w.memberExists(username); err != nil {
		return err
	}
	session, err := w.api.Login(username, harness.Password)
	if err != nil {
		return err
	}
	w.sessions[username] = session
	return nil
}

func (w *world) openManageUsers() error {
	if err := w.click(manageUsersLink); err != nil {
		return err
	}
	_, err := w.element(userList)
	return err
}

func (w *world) createUser(username string) error {
	if err := w.fill(newUserForm+" input[name=username]", username); err != nil {
		return err
	}
	if err := w.fill(newUserForm+" input[name=password]", harness.Password); err != nil {
		return err
	}
	return w.click(newUserForm + " button[type=submit]")
}

func (w *world) startResettingPassword(username string) error {
	if err := w.click(fmt.Sprintf("a[aria-label='Reset password of %s']", username)); err != nil {
		return err
	}
	_, err := w.element(passwordForm)
	return err
}

func (w *world) resetPassword(username, password string) error {
	if err := w.startResettingPassword(username); err != nil {
		return err
	}
	if err := w.fill(passwordForm+" input[name=password]", password); err != nil {
		return err
	}
	return w.click(passwordForm + " button[type=submit]")
}

func (w *world) startDeletingUser(username string) error {
	return w.click(fmt.Sprintf("a[aria-label='Delete %s']", username))
}

func (w *world) deleteUser(username string) error {
	if err := w.startDeletingUser(username); err != nil {
		return err
	}
	return w.click("main button.danger-button")
}

func userRow(username string) harness.Locator {
	return harness.XPath(fmt.Sprintf(
		"//ul[@class='people']/li[span[@class='who' and text()=%s]]", xpathString(username)))
}

func (w *world) userListShowsDisabled(username string) error {
	return harness.Eventually(username+" is listed as disabled", func() (bool, error) {
		row, err := w.browser.First(userRow(username))
		if err != nil {
			return false, err
		}
		text, err := row.Text()
		return strings.Contains(text, "Disabled"), err
	})
}

func (w *world) sessionNoLongerWorks(username string) error {
	session, err := w.session(username)
	if err != nil {
		return err
	}
	user, err := w.api.SignedInUser(session)
	if err != nil {
		return err
	}
	if user != "" {
		return fmt.Errorf("%s is still signed in", username)
	}
	return nil
}

func (w *world) cannotOpenManageUsers() error {
	if _, err := w.element(".me strong"); err != nil {
		return err
	}
	links, err := w.browser.All(harness.CSS(manageUsersLink))
	if err != nil {
		return err
	}
	if len(links) != 0 {
		return fmt.Errorf("a Manage users link is offered")
	}
	return nil
}

func (w *world) rowHasNoControls(username string) error {
	row, err := w.browser.Await(userRow(username))
	if err != nil {
		return err
	}
	controls, err := row.All(harness.CSS("button, a"))
	if err != nil {
		return err
	}
	if len(controls) != 0 {
		return fmt.Errorf("the row offers %d controls", len(controls))
	}
	return nil
}
