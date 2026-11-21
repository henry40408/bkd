Feature: The list as a side index

  Background:
    Given I am signed in as the admin "root"

  Scenario: Load more by keyboard grows the list in place and moves the focus to the first new row
    Given I have 51 bookmarks
    When I open the site
    And I mark the page
    And I focus Load more
    And I press Enter
    Then the address is "/?page=2"
    And the page was not reloaded
    And the list has 51 bookmarks
    And the focus is on "ul.list li.item:nth-child(51) h3 a"

  Scenario: Load more in select mode keeps the ticks and offers Select all
    Given I have 51 bookmarks
    When I open the site at "/?select=1"
    Then the bulk bar shows "Select the 50 shown"
    When I tick "Bulk 51"
    And I tick "Bulk 50"
    And I mark the page
    And I focus Load more
    And I press Enter
    Then the address is "/?page=2&select=1"
    And the page was not reloaded
    And the list has 51 bookmarks
    And "Bulk 51" is ticked
    And "Bulk 50" is ticked
    And "Bulk 49" is not ticked
    And "Bulk 1" is not ticked
    And the selection count says "2 selected"
    And the bulk bar shows "Select all"
    And the focus is on "ul.list li.item:nth-child(51) .pick-box input"

  Scenario: Load more keeps the tag filter
    Given I have 51 bookmarks tagged "x"
    And I have the bookmark "https://other.test" titled "Other"
    When I open the site at "/?tag=x"
    Then the heading says "Tagged “x”"
    And the count says "51 of 52"
    And the list has 50 bookmarks
    When I choose Load more
    Then the address is "/?tag=x&page=2"
    And the list has 51 bookmarks
    And the heading says "Tagged “x”"
    And the count says "51 of 52"
    And the list shows "All 51 bookmarks shown"
    And the list does not show "Other"

  Scenario: Choosing a tag names the list, and its chip removes the filter
    Given I have the bookmark "https://a.test" titled "Alpha" tagged "rust"
    And I have the bookmark "https://b.test" titled "Beta"
    When I open the site
    Then the heading says "All bookmarks"
    And there is no filter chip
    When I choose the tag "rust"
    Then the address is "/?tag=rust"
    And the heading says "Tagged “rust”"
    And the filter chip says "Tag: rust"
    And the list does not show "Beta"
    When I choose the filter chip "Tag: rust"
    Then the address is "/"
    And the heading says "All bookmarks"
    And there is no filter chip
    And the list shows "Beta"

  Scenario: Choosing a link status names the list, and its chip removes the filter
    Given I have the bookmark "https://dead.test" titled "Dead" whose link is broken
    And I have the bookmark "https://alive.test" titled "Alive"
    When I open the site
    And I choose the link filter "Broken"
    Then the address is "/?status=broken"
    And the heading says "Broken links"
    And the filter chip says "Status: Broken"
    And the list shows "Dead"
    And the list does not show "Alive"
    When I choose the filter chip "Status: Broken"
    Then the address is "/"
    And the heading says "All bookmarks"
    And there is no filter chip
    And the list shows "Alive"

  Scenario: Editing in the random pick opens the form inside the pick
    Given I have the bookmark "https://a.test" titled "Alpha"
    When I open the site
    And I choose Random
    And I choose Edit in the pick
    Then the edit form is inside the pick
    When I change the title in the edit form to "Renamed"
    And I save the edit
    Then I am told "Bookmark saved."
    And the list shows "Renamed"
    And the list does not show "Alpha"

  Scenario: Deleting in the random pick asks inline, and keeping returns the focus to Delete
    Given I have the bookmark "https://a.test" titled "Alpha"
    When I open the site
    And I choose Random
    And I choose Delete in the pick
    Then I am asked "Delete this bookmark?"
    When I keep it
    Then the delete question is closed
    And the focus is on "section.pick .btns a[href*='/delete']"
    And the pick shows "Alpha"
    When I choose Delete in the pick
    And I confirm the deletion
    Then I am told "Bookmark deleted."
    And the list does not show "Alpha"
    And the account has exactly 0 bookmarks

  Scenario: The slash key jumps to the search field
    Given I have the bookmark "https://a.test" titled "Alpha"
    When I open the site
    And I watch the keys
    And I type "/"
    Then the focus is on "[role=search] input[name=q]"
    And the page took the slash key

  Scenario: A slash typed in a field of the add form stays in the field
    When I open the site
    And I focus the "note" field of the add form
    And I type "a/b"
    Then the "note" field of the add form holds "a/b"
    And the focus is on "#add textarea[name=note]"
    When I focus the "title" field of the add form
    And I type "c/d"
    Then the "title" field of the add form holds "c/d"
    And the focus is on "#add input[name=title]"
