Feature: Bulk actions

  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "https://a.test" titled "Alpha" tagged "rust"
    And I have the bookmark "https://b.test" titled "Beta"
    And I have the bookmark "https://c.test" titled "Gamma" tagged "go"

  Scenario: Checkboxes show only in select mode
    When I open the site
    Then select mode is off
    And the list does not show "Select all on this page"
    When I enter select mode
    Then select mode is on
    And the address is "/?select=1"
    And the selection count says "0 selected"
    When I leave select mode
    Then select mode is off
    And the address is "/"

  Scenario: Ticking rows counts them, and Select all ticks every row on the page
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    Then the selection count says "1 selected"
    And "Alpha" is ticked
    And "Beta" is not ticked
    When I select all on this page
    Then the selection count says "3 selected"
    And "Beta" is ticked
    When I clear the selection
    Then the selection count says "0 selected"
    And "Alpha" is not ticked

  Scenario: Shift-click ticks the rows in between
    When I open the site
    And I enter select mode
    And I tick "Gamma"
    And I Shift-click "Alpha"
    Then the selection count says "3 selected"
    And "Beta" is ticked

  Scenario: One tag added to several bookmarks, then select mode ends
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    And I tick "Beta"
    And I add the tag "later" to the selection
    Then I am told "Done."
    And select mode is off
    When I choose the tag "later"
    Then the list shows "Alpha"
    And the list shows "Beta"
    And the list does not show "Gamma"

  Scenario: Tags are split at commas, as in the add form
    When I open the site
    And I enter select mode
    And I tick "Beta"
    And I add the tag "one, two" to the selection
    Then I am told "Done."
    When I choose the tag "two"
    Then the list shows "Beta"

  Scenario: A tag removed from several bookmarks
    Given I have the bookmark "https://d.test" titled "Delta" tagged "rust"
    When I open the site
    And I enter select mode
    And I select all on this page
    And I remove the tag "rust" from the selection
    Then I am told "Done."
    And select mode is off
    And the sidebar lists the tag "go"
    And the sidebar does not list the tag "rust"

  Scenario: Removing a tag nobody has is explained and keeps the selection
    When I open the site
    And I enter select mode
    And I tick "Beta"
    And I remove the tag "rust" from the selection
    Then I see the error "None of the selected bookmarks has that tag."
    And select mode is on
    And "Beta" is ticked
    And "Alpha" is not ticked
    And the selection count says "1 selected"
    And the tags field holds "rust"

  Scenario: An empty tag is refused and keeps the selection
    When I open the site
    And I enter select mode
    And I tick "Beta"
    And I add the tag " , " to the selection
    Then I see the error "tag"
    And "Beta" is ticked
    And the selection count says "1 selected"

  Scenario: Choosing an action with nothing ticked says so
    When I open the site
    And I enter select mode
    And I add the tag "later" to the selection
    Then I am told "Tick at least one bookmark first."
    And the address is "/?select=1&notice=nothing-selected"

  Scenario: Deleting the selection asks first and can be called off
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    And I tick "Beta"
    And I start deleting the selection
    Then I am asked "Delete 2 bookmarks?"
    When I keep it
    Then the address is "/?select=1"
    And the list shows "Alpha"
    And the account has exactly 3 bookmarks
    When I tick "Alpha"
    And I tick "Beta"
    And I start deleting the selection
    And I confirm the deletion
    Then I am told "Done."
    And the list does not show "Alpha"
    And the list does not show "Beta"
    And the list shows "Gamma"
    And select mode is off
    And the account has exactly 1 bookmark

  Scenario: Escape leaves select mode
    When I open the site
    And I enter select mode
    And I tick "Beta"
    And I press Escape
    Then select mode is off
    And the address is "/"

  Scenario: Choosing a tag in the sidebar leaves select mode and clears the selection
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    And I choose the tag "go"
    Then the address is "/?tag=go"
    And the list shows "Gamma"
    And select mode is off

  Scenario: Ticking a row keeps the keyboard focus on its checkbox
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    Then the focus is on "input[aria-label='Select Alpha']"
