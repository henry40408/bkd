Feature: Link check

  # The server only contacts public addresses, so these scenarios use a private one.
  # What the check finds on real answers (ok, redirected, broken) is covered by the
  # server's own tests, which can run a web server on the same machine.
  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "http://127.0.0.1:1/page" titled "Private"
    And I have the bookmark "https://example.test/fine" titled "Other"

  Scenario: Checking selected links shows what could not be confirmed
    When I open the site
    And I enter select mode
    And I tick "Private"
    And I check the links of the selection
    Then I am told "Checked 1 bookmark"
    And the list shows "Address is not public"
    And "Private" is ticked

  Scenario: Progress shows while the check runs
    When I open the site
    And I enter select mode
    And I tick "Private"
    And the server answers requests for "/api/bookmarks/check" slowly
    And I check the links of the selection
    Then the link check shows "Checking links: 0 of 1"
    And I am told "Checked 1 bookmark"
    And the list shows "Address is not public"

  Scenario: Stop ends the check and keeps the selection
    When I open the site
    And I enter select mode
    And I tick "Private"
    And the server answers requests for "/api/bookmarks/check" slowly
    And I check the links of the selection
    Then the link check shows "Checking links: 0 of 1"
    When I stop the link check
    Then I am told "Checked 0 of 1 bookmark"
    And "Private" is ticked
    And the list does not show "Address is not public"

  Scenario: The result can be searched, and an unchecked bookmark is not "broken"
    When I open the site
    And I enter select mode
    And I tick "Private"
    And I check the links of the selection
    Then the list shows "Address is not public"
    When I search for "status:unknown"
    Then the list shows "Private"
    And the list does not show "Other"
    When I search for "status:unchecked"
    Then the list shows "Other"
    And the list does not show "Private"
    When I search for "-status:broken"
    Then the list shows "Private"
    And the list shows "Other"

  Scenario: The sidebar lists the results and filters by one
    When I open the site
    And I enter select mode
    And I tick "Private"
    And I check the links of the selection
    Then the sidebar lists the link filter "Not confirmed"
    And the sidebar lists the link filter "Not checked"
    When I choose the link filter "Not confirmed"
    Then the address is "/?status=unknown"
    And the list shows "Private"
    And the list does not show "Other"
    When I choose the link filter "Not checked"
    Then the list shows "Other"
    And the list does not show "Private"

  Scenario: The edit form offers the address a moved link went to
    Given I have the bookmark "https://old.test/page" titled "Moved one" whose link moved to "https://new.test/page"
    When I open the site
    Then the list shows "Link moved"
    When I start editing "Moved one"
    Then the edit form says "This link moved to https://new.test/page"
    And the new address button is offered
    When I use the new address
    Then the edit form holds "https://new.test/page" as the URL
    And the new address button is not offered
    And the edit form says "New address filled in. Save to keep it; the link is checked again later."
    And the focus is on ".edit-form input[name=url]"
    When I save the edit
    Then I am told "Bookmark saved."
    And the list shows "https://new.test/page"
    And the list does not show "Link moved"
