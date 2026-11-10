Feature: Bookmarks

  Background:
    Given I am signed in as the admin "root"

  Scenario: Adding a bookmark
    When I add the bookmark "https://example.com" titled "Example" tagged "rust, web"
    Then the list shows "Example"
    And the link "Example" points to "https://example.com/"
    And the sidebar lists the tag "rust"

  Scenario: A URL without a scheme is refused with a hint
    When I add the bookmark "example.com" titled "Example" tagged "rust"
    Then I see the error "Enter a full URL"
    And the account has exactly 0 bookmarks

  Scenario: Submitting the add form twice creates one bookmark
    When I press Enter twice to add "https://twice.test" titled "Twice"
    Then the list shows "Twice"
    And the browser reports no policy violation
    And the account has exactly 1 bookmark

  Scenario: Editing a bookmark
    Given I have the bookmark "https://old.test" titled "Old title"
    When I open the site
    And I edit "Old title" to the title "New title"
    Then the list shows "New title"
    And the list does not show "Old title"

  Scenario: Deleting a bookmark asks first
    Given I have the bookmark "https://gone.test" titled "Gone soon"
    When I open the site
    And I start deleting "Gone soon"
    And I keep it
    Then the list shows "Gone soon"
    And the account has exactly 1 bookmark
    When I start deleting "Gone soon"
    And I confirm the deletion
    Then the list does not show "Gone soon"

  Scenario: Search finds a bookmark by its note and marks the match
    Given I have the bookmark "https://a.test" titled "Alpha" with the note "about ferris"
    And I have the bookmark "https://b.test" titled "Beta"
    When I open the site
    And I search for "ferris"
    Then the list shows "Alpha"
    And the list does not show "Beta"
    And "ferris" is highlighted

  Scenario: A tag filter plus a search that finds nothing names both
    Given I have the bookmark "https://a.test" titled "Alpha" tagged "rust"
    When I open the site
    And I choose the tag "rust"
    And I search for "zzz"
    Then I see the empty message "No bookmarks tagged “rust” match “zzz”"

  Scenario: Show more appears only when there is more
    Given I have 50 bookmarks
    When I open the site
    Then the list has 50 bookmarks
    And there is no Show more button
    Given I have 1 more bookmark
    When I open the site
    Then the list has 50 bookmarks
    And there is a Show more button
    When I choose Show more
    Then the list has 51 bookmarks

  Scenario: Show more goes back to one page after a reload
    Given I have 51 bookmarks
    When I open the site
    And I choose Show more
    Then the list has 51 bookmarks
    When I open the site
    Then the list has 50 bookmarks
    And there is a Show more button

  Scenario: A slow answer to an older search never replaces a newer one
    Given I have the bookmark "https://a.test" titled "Alpha"
    And I have the bookmark "https://b.test" titled "Alamo"
    When I open the site
    And the server answers the search for "al" slowly
    And I search for "al"
    Then the search for "al" has been sent
    When I search for "alp"
    Then the list does not show "Alamo"
    And no slow answer has arrived yet
    When the slow answer to the search for "al" has arrived
    Then the list does not show "Alamo"
    And the list shows "Alpha"

  Scenario: Text from a bookmark is shown as text, never run
    Given I have the bookmark "https://x.test" titled "<img src=x onerror=document.title='pwned'>"
    When I open the site
    Then the list shows "<img src=x onerror=document.title='pwned'>"
    And the page title is still "bkd"
