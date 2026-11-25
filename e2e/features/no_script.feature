Feature: The pages work without a script

  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "https://a.test" titled "Alpha" tagged "rust"
    And I have the bookmark "https://b.test" titled "Beta"
    And scripts are switched off

  Scenario: Signing in and out with plain forms
    When I sign out
    Then I see the sign-in page
    When I sign in as "root" with the right password
    Then I am signed in as "root"
    And the address is "/"

  Scenario: Adding, editing and deleting a bookmark with plain forms
    When I open the site
    And I add the bookmark "https://c.test" titled "Gamma" tagged "go"
    Then I am told "Bookmark saved."
    And the list shows "Gamma"
    When I edit "Gamma" to the title "Delta"
    Then the list shows "Delta"
    And the list does not show "Gamma"
    When I start deleting "Delta"
    And I confirm the deletion
    Then I am told "Bookmark deleted."
    And the list does not show "Delta"

  Scenario: Searching and choosing a tag are plain links and forms
    When I open the site
    And I search for "alpha"
    Then the address is "/?q=alpha"
    And the list shows "Alpha"
    And the list does not show "Beta"
    When I open the site
    And I choose the tag "rust"
    Then the address is "/?tag=rust"
    And the list does not show "Beta"

  Scenario: Load more works with a plain link
    Given I have 50 more bookmarks
    When I open the site
    And I choose Load more
    Then the address is "/?page=2"
    And the list has 52 bookmarks
    And Load more is not offered

  Scenario: Bulk actions work with plain checkboxes and forms
    When I open the site
    And I enter select mode
    Then the selection count is blank
    When I select all
    Then "Alpha" is ticked
    And "Beta" is ticked
    When I clear the selection
    Then "Alpha" is not ticked
    When I tick "Alpha"
    And I add the tag "later" to the selection
    Then I am told "Done."
    When I choose the tag "later"
    Then the list shows "Alpha"
    And the list does not show "Beta"

  Scenario: A refused bulk action keeps the ticks
    When I open the site
    And I enter select mode
    And I tick "Beta"
    And I remove the tag "rust" from the selection
    Then I see the error "None of the selected bookmarks has that tag."
    And "Beta" is ticked

  Scenario: Deleting the selection asks on its own page
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    And I start deleting the selection
    Then I am asked "Delete 1 bookmark?" on its own page
    When I confirm the deletion
    Then I am told "Done."
    And the list does not show "Alpha"
    And the list shows "Beta"

  Scenario: The link check waits for the result and shows the list again
    Given I have the bookmark "http://127.0.0.1:1/page" titled "Private"
    When I open the site
    And I enter select mode
    And I tick "Private"
    And I check the links of the selection
    Then I am told "Done."
    And the list shows "Address is not public"

  Scenario: Reading pages waits for the result and keeps the pages that were not read ticked
    Given I have the bookmark "http://127.0.0.1:1/page" titled "Private"
    When I open the site
    And I enter select mode
    And I tick "Private"
    And I read the pages of the selection
    Then I am told "1 page was not read"
    And "Private" is ticked

  Scenario: An admin creates a user with plain forms
    When I open Manage users
    And I create the user "mei" with a temporary password
    Then I am told "Saved."
    And the user list shows "mei"

  Scenario: The password form works with plain forms
    When I open Settings
    And I change my password from "correct-horse-battery" to "a-brand-new-password" but repeat "a-brand-new-passwore"
    Then I see the error "are different"

  Scenario: The edit page of a moved link shows where it went but has no button
    Given I have the bookmark "https://old.test/page" titled "Moved one" whose link moved to "https://new.test/page"
    When I open the site
    And I start editing "Moved one"
    Then the edit form says "This link moved to https://new.test/page"
    And the new address button is not offered

  Scenario: The import page does not offer dropping a file
    When I open Import and export
    Then the import box does not offer dropping a file
