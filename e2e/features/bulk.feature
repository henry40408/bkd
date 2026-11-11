Feature: Bulk actions

  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "https://a.test" titled "Alpha" tagged "rust"
    And I have the bookmark "https://b.test" titled "Beta"
    And I have the bookmark "https://c.test" titled "Gamma" tagged "go"

  Scenario: Checkboxes show only in select mode
    When I open the site
    Then select mode is off
    And the list does not show "Select Alpha"
    When I enter select mode
    Then select mode is on
    And the selection count says "Select bookmarks"
    When I leave select mode
    Then select mode is off

  Scenario: Ticking rows counts them, and Select all ticks every loaded row
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    Then the selection count says "1 selected"
    And "Alpha" is ticked
    And "Beta" is not ticked
    When I select all shown bookmarks
    Then the selection count says "3 selected"
    When I select all shown bookmarks
    Then the selection count says "Select bookmarks"
    And "Alpha" is not ticked

  Scenario: Shift-click ticks the rows in between
    When I open the site
    And I enter select mode
    And I tick "Gamma"
    And I Shift-click "Alpha"
    Then the selection count says "3 selected"

  Scenario: One tag added to several bookmarks, then select mode ends
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    And I tick "Beta"
    And I add the tag "later" to the selection
    Then I am told "Added “later” to 2 bookmarks"
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
    Then I am told "Added “one”, “two” to 1 bookmark"
    When I choose the tag "two"
    Then the list shows "Beta"

  Scenario: A tag removed from several bookmarks
    Given I have the bookmark "https://d.test" titled "Delta" tagged "rust"
    When I open the site
    And I enter select mode
    And I select all shown bookmarks
    And I remove the tag "rust" from the selection
    Then I am told "Removed “rust” from 2 bookmarks"
    And select mode is off
    And the sidebar lists the tag "go"

  Scenario: Removing a tag nobody has is explained and keeps the selection
    When I open the site
    And I enter select mode
    And I tick "Beta"
    And I remove the tag "rust" from the selection
    Then the bulk bar shows "None of the selected bookmarks has that tag."
    And select mode is on
    And "Beta" is ticked

  Scenario: An empty tag is refused before anything is sent
    When I open the site
    And I enter select mode
    And I tick "Beta"
    And I add the tag " , " to the selection
    Then the bulk bar shows "Type a tag first."
    And "Beta" is ticked

  Scenario: Deleting the selection asks first and can be called off
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    And I tick "Beta"
    And I start deleting the selection
    Then the bulk bar shows "Delete 2 bookmarks?"
    When I keep the selection
    Then the bulk bar does not show "Delete 2 bookmarks?"
    And the selection count says "2 selected"
    And the account has exactly 3 bookmarks
    When I start deleting the selection
    And I confirm deleting the selection
    Then I am told "Deleted 2 bookmarks"
    And the list does not show "Alpha"
    And the list does not show "Beta"
    And the list shows "Gamma"
    And select mode is off
    And the account has exactly 1 bookmark

  Scenario: Unticking the last row closes the panel
    When I open the site
    And I enter select mode
    And I tick "Beta"
    And I start deleting the selection
    And I tick "Beta"
    Then the bulk bar does not show "Delete 0 bookmarks?"
    And the selection count says "Select bookmarks"

  Scenario: Escape closes the open panel first, then leaves select mode
    When I open the site
    And I enter select mode
    And I tick "Beta"
    And I start deleting the selection
    And I press Escape
    Then the bulk bar does not show "Delete 1 bookmark?"
    And select mode is on
    When I press Escape
    Then select mode is off

  Scenario: Choosing another tag clears the selection but stays in select mode
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    And I choose the tag "go"
    Then the selection count says "Select bookmarks"
    And select mode is on
    And the list shows "Gamma"
    And "Gamma" is not ticked

  Scenario: Ticking a row keeps the keyboard focus on its checkbox
    When I open the site
    And I enter select mode
    And I tick "Alpha"
    Then the focus is on "input[aria-label='Select Alpha']"
