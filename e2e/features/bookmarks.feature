Feature: Bookmarks

  Background:
    Given I am signed in as the admin "root"

  Scenario: An empty account says how to start
    When I open the site
    Then I see the empty message "No bookmarks yet."

  Scenario: Adding a bookmark
    When I add the bookmark "https://example.com" titled "Example" tagged "rust, web"
    Then I am told "Bookmark saved."
    And the address is "/?notice=bookmark-saved"
    And the list shows "Example"
    And the link "Example" points to "https://example.com"
    And the sidebar lists the tag "rust"

  Scenario: A URL without a scheme is stopped by the browser
    When I add the bookmark "example.com" titled "Example" tagged "rust"
    Then the browser refuses the field "#add input[name=url]"
    And the address is "/"
    And the account has exactly 0 bookmarks

  Scenario: The server refuses a URL it cannot save and keeps what was typed
    When I open the site
    And the browser's own form checks are switched off
    And I add the bookmark "example.com" titled "Example" tagged "rust"
    Then I see the error "must start with http"
    And the add form holds "Example" as the title
    And the account has exactly 0 bookmarks

  Scenario: Submitting the add form twice creates one bookmark
    When I open the site
    And I press Enter twice to add "https://twice.test" titled "Twice"
    Then the list shows "Twice"
    And the browser reports no policy violation
    And the account has exactly 1 bookmark

  Scenario: Editing a bookmark returns to the list
    Given I have the bookmark "https://old.test" titled "Old title"
    When I open the site
    And I edit "Old title" to the title "New title"
    Then I am told "Bookmark saved."
    And the list shows "New title"
    And the list does not show "Old title"

  Scenario: Cancelling an edit returns to the list I came from
    Given I have the bookmark "https://a.test" titled "Zebra" tagged "stripes"
    And I have the bookmark "https://b.test" titled "Other" tagged "plain"
    When I open the site at "/?tag=stripes"
    And I start editing "Zebra"
    And I cancel editing
    Then the address is "/?tag=stripes"
    And the list shows "Zebra"
    And the list does not show "Other"

  Scenario: Saving an edit returns to the list I came from
    Given I have the bookmark "https://a.test" titled "Zebra" tagged "stripes"
    And I have the bookmark "https://b.test" titled "Other" tagged "plain"
    When I open the site at "/?tag=stripes"
    And I edit "Zebra" to the title "Okapi"
    Then the address is "/?tag=stripes&notice=bookmark-saved"
    And the list shows "Okapi"
    And the list does not show "Other"

  Scenario: The age of a bookmark shows its exact date on hover
    Given I have the bookmark "https://dated.test" titled "Dated"
    When I open the site
    Then the age of "Dated" says "just now"
    And hovering the age of "Dated" shows the date as the browser formats it

  Scenario: Deleting a bookmark asks first
    Given I have the bookmark "https://gone.test" titled "Gone soon"
    When I open the site
    And I start deleting "Gone soon"
    Then I am asked "Delete this bookmark?"
    And the list shows "Gone soon"
    When I keep it
    Then the address is "/"
    And the list shows "Gone soon"
    And the account has exactly 1 bookmark
    When I start deleting "Gone soon"
    And I confirm the deletion
    Then I am told "Bookmark deleted."
    And the list does not show "Gone soon"
    And the account has exactly 0 bookmarks

  Scenario: Keeping a bookmark returns to the list I came from
    Given I have the bookmark "https://a.test" titled "Zebra" tagged "stripes"
    When I open the site at "/?tag=stripes"
    And I start deleting "Zebra"
    And I keep it
    Then the address is "/?tag=stripes"

  Scenario: Search finds a bookmark by its note and marks the match
    Given I have the bookmark "https://a.test" titled "Alpha" with the note "about ferris"
    And I have the bookmark "https://b.test" titled "Beta"
    When I open the site
    And I search for "ferris"
    Then the address is "/?q=ferris"
    And the list shows "Alpha"
    And the list does not show "Beta"
    And "ferris" is highlighted

  Scenario: Search syntax narrows by phrase, exclusion and field
    Given I have the bookmark "https://a.test" titled "Quick brown fox" tagged "to read"
    And I have the bookmark "https://b.test" titled "Brown quick fox" tagged "done"
    And I have the bookmark "https://zebra.test" titled "Plain page"
    When I open the site
    And I search for '"quick brown"'
    Then the address is "/?q=%22quick+brown%22"
    And the list shows "Quick brown fox"
    And the list does not show "Brown quick fox"
    When I search for "fox -done"
    Then the address is "/?q=fox+-done"
    And the list shows "Quick brown fox"
    And the list does not show "Brown quick fox"
    When I search for 'tag:"to read"'
    Then the address is "/?q=tag%3A%22to+read%22"
    And the list shows "Quick brown fox"
    And the list does not show "Plain page"
    When I search for "title:zebra"
    Then I see the empty message "No bookmarks match."
    When I search for "url:zebra"
    Then the list shows "Plain page"

  Scenario: A field prefix marks matches only in that field
    Given I have the bookmark "https://zebra.test" titled "zebra stripes"
    When I open the site
    And I search for "title:zebra"
    Then "zebra" is highlighted
    When I search for "title:stripes -title:zebra"
    Then I see the empty message "No bookmarks match."
    When I search for "stripes"
    Then "stripes" is highlighted
    And "zebra" is not highlighted

  Scenario: Random picks from what the tag filter shows
    Given I have the bookmark "https://a.test" titled "Alpha" tagged "rust"
    And I have the bookmark "https://b.test" titled "Beta" tagged "go"
    When I open the site
    And I choose the tag "rust"
    And I choose Random
    Then the pick shows "Alpha"
    And the pick does not show "Beta"
    When I choose Another
    Then the pick shows "Alpha"
    When I close the pick
    Then there is no pick
    And the address is "/?tag=rust"

  Scenario: Random picks from what the search finds, and a new search clears the pick
    Given I have the bookmark "https://a.test" titled "Alpha"
    And I have the bookmark "https://b.test" titled "Beta"
    When I open the site
    And I search for "beta"
    And I choose Random
    Then the pick shows "Beta"
    When I search for "alpha"
    Then there is no pick

  Scenario: Random says so when nothing matches
    Given I have the bookmark "https://a.test" titled "Alpha"
    When I open the site
    And I search for "zzz"
    And I choose Random
    Then I am told "Nothing to pick from here."

  Scenario: A tag filter plus a search that finds nothing says so
    Given I have the bookmark "https://a.test" titled "Alpha" tagged "rust"
    When I open the site
    And I choose the tag "rust"
    And I search for "zzz"
    Then I see the empty message "No bookmarks match."
    And the address is "/?q=zzz&tag=rust"

  Scenario: The pager appears only when there is more
    Given I have 50 bookmarks
    When I open the site
    Then the list has 50 bookmarks
    And the pager does not offer "Older"
    And the pager does not offer "Newer"
    Given I have 1 more bookmark
    When I open the site
    Then the list has 50 bookmarks
    And the pager offers "Older"
    And the pager does not offer "Newer"

  Scenario: Older and Newer move between pages
    Given I have 51 bookmarks
    When I open the site
    And I choose "Older" in the pager
    Then the address is "/?page=2"
    And the list has 1 bookmarks
    And the list shows "Showing 51–51"
    And the pager offers "Newer"
    And the pager does not offer "Older"
    When I choose "Newer" in the pager
    Then the address is "/"
    And the list has 50 bookmarks
    And the pager offers "Older"

  Scenario: Editing on the second page returns to the second page
    Given I have 51 bookmarks
    When I open the site at "/?page=2"
    And I edit "Bulk 1" to the title "First one"
    Then the address is "/?page=2&notice=bookmark-saved"
    And the list shows "First one"

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
