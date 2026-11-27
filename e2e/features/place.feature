Feature: Keeping the reader's place on a phone

  Background:
    Given I am signed in as the admin "root"
    And I have 30 bookmarks
    And the window is phone-sized

  Scenario: Opening and closing the menu leaves the list where it is
    When I open the site
    And I watch where "Bulk 15" is
    And I open the menu
    Then the sidebar is visible
    And "Bulk 15" is where it was at the last tap
    When I tap the backdrop
    Then the sidebar is hidden
    And "Bulk 15" is where it was at the last tap

  Scenario: Cancelling an edit puts the row back
    When I open the site
    And I watch where "Bulk 15" is
    And I tap the row action "Edit Bulk 15" where it is
    And I cancel editing
    Then the edit form is closed
    And "Bulk 15" is where it was at the first tap

  Scenario: Saving an edit puts the row back
    When I open the site
    And I watch where "Bulk 15" is
    And I start editing "Bulk 15"
    And I change the title in the edit form to "Renamed"
    And I save the edit
    Then the list shows "Renamed"
    And the edit form is closed
    And "Renamed" is where it was at the first tap

  Scenario: A refused edit still knows where the row was
    When I open the site
    And I watch where "Bulk 15" is
    And I tap the row action "Edit Bulk 15" where it is
    And I save the edit with the URL "ftp://bulk.test/15"
    Then the edit form says "url must start with"
    When I cancel editing
    Then the edit form is closed
    And "Bulk 15" is where it was at the first tap

  Scenario: Keeping a bookmark puts the row back
    When I open the site
    And I watch where "Bulk 14" is
    And I start deleting "Bulk 14"
    And I keep it
    Then the delete question is closed
    And "Bulk 14" is where it was at the first tap

  Scenario: Deleting a bookmark lets the next one take its place
    When I open the site
    And I watch where "Bulk 15" is
    And I start deleting "Bulk 14"
    And I confirm the deletion
    Then the list does not show "Bulk 14"
    And "Bulk 15" is where it was at the first tap

  Scenario: Select all and Done keep the list in place
    When I open the site at "/?select=1"
    And I watch where "Bulk 15" is
    And I tap "Select all" in the bulk bar where it is
    Then the selection count says "30 selected"
    And the top row is where it was at the last tap
    When I tap "Done" in the bulk bar where it is
    Then select mode is off
    And the top row is where it was at the last tap

  Scenario: A bulk action keeps the ticked row in place
    When I open the site at "/?select=1"
    And I watch where "Bulk 15" is
    And I tick "Bulk 15"
    And I fill in the tags "later" for the selection
    And I tap "Add tags" in the bulk bar where it is
    Then I am told "Done."
    And select mode is off
    And "Bulk 15" is where it was at the last tap

  Scenario: Deleting the selection stays on the page and in place
    When I open the site at "/?select=1"
    And I mark the page
    And I watch where "Bulk 15" is
    And I tick "Bulk 14"
    And I tap "Delete" in the bulk bar where it is
    And I confirm the deletion where the button is
    Then the list does not show "Bulk 14"
    And the page was not reloaded
    And the top row is where it was at the last tap

  Scenario: Going back from a shorter page returns to the place in the list
    # On its own the browser would scroll the Settings page, much shorter than the
    # list, before the list is there again.
    Given I have 20 more bookmarks
    When I open the site
    And I watch where "Bulk 3" is
    And I open the menu
    And I open Settings
    And pages are slow to arrive
    And I go back
    Then "Bulk 3" is where it was at the last tap
    And the page on screen did not scroll while the next one arrived

  Scenario: Going forward and back again keeps each page's place
    Given I have 20 more bookmarks
    When I open the site
    And I open the menu
    And I open Settings
    And I go back
    And I read on to "Bulk 3"
    And I go forward
    Then the settings page is shown
    When I go back
    Then the page is scrolled as far as noted

  Scenario: Reloading the list keeps the place
    When I open the site
    And I read on to "Bulk 15"
    And I reload the page
    Then the page is scrolled as far as noted

  Scenario: Going back after a reload returns to the place in the list
    Given I have 20 more bookmarks
    When I open the site
    And I watch where "Bulk 3" is
    And I open the menu
    And I open Settings
    And I note how far the page was scrolled at the last tap
    And I reload the page
    And I go back
    Then the page is scrolled as far as noted

  Scenario: Going back past another page returns to the place in the list
    # Two steps at once load the list afresh: nothing is left to swap into.
    Given I have 20 more bookmarks
    When I open the site
    And I watch where "Bulk 3" is
    And I open the menu
    And I open Settings
    And I note how far the page was scrolled at the last tap
    And I open the site at "/api/docs"
    And I go back two pages
    Then the page is scrolled as far as noted

  Scenario: A page the phone drops keeps the place
    When I open the site
    And I read on to "Bulk 15"
    And the phone drops the page while the browser is away
    And I reload the page
    Then the page is scrolled as far as noted

  Scenario: Selecting in a search that ends in a space keeps the place
    When I open the site at "/?q=bulk+&select=1"
    And I watch where "Bulk 15" is
    And I tap "Done" in the bulk bar where it is
    Then select mode is off
    And the top row is where it was at the last tap

  Scenario: On a touch screen the edit panel takes the focus, not a field
    Given the screen is a touch screen
    When I open the site
    And I watch where "Bulk 15" is
    And I tap the row action "Edit Bulk 15" where it is
    Then the focus is on ".item .inline-form"
    When I cancel editing
    Then the edit form is closed
    And "Bulk 15" is where it was at the first tap
