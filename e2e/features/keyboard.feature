Feature: Keyboard focus

  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "https://a.test" titled "Alpha"

  Scenario: Editing puts the focus in the form
    When I open the site
    And I start editing "Alpha"
    Then the focus is on ".edit-form input[name=url]"

  Scenario: Asking to delete puts the focus on Keep
    When I open the site
    And I start deleting "Alpha"
    Then the focus is on "main a.quiet-link"

  Scenario: Keeping a bookmark by keyboard leaves the focus on the page
    When I open the site
    And I start deleting "Alpha"
    Then the focus is on "main a.quiet-link"
    When I press Enter
    Then the address is "/"
    And the list shows "Alpha"
    And the focus is on "#main"

  Scenario: Deleting a bookmark does not drop the focus to the top of the page
    When I open the site
    And I start deleting "Alpha"
    And I confirm the deletion
    Then I am told "Bookmark deleted."
    And the focus is on "#main"

  Scenario: Resetting a password puts the focus in the password field
    Given the member "mei" exists
    When I open the site
    And I open Manage users
    And I start resetting the password of "mei"
    Then the focus is on ".password-form input[name=password]"

  Scenario: Confirming the password puts the focus in the password field
    Given the member "mei" exists
    And my password confirmation is old
    When I open the site at "/users"
    And I disable "mei"
    Then I am asked to confirm my password
    And the focus is on "form[action='/confirm'] input[name=password]"
