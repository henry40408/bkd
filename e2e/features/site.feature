Feature: Page details

  # The server only contacts public addresses, so a scenario cannot serve a page
  # to read. What a page's title and description turn into (Open Graph, title,
  # description, limits) is covered by the server's own tests, which can run a web
  # server on the same machine. These scenarios use a private address for the
  # failure, and put the stored details in place for what the edit form shows.
  Background:
    Given I am signed in as the admin "root"

  Scenario: An address the server may not read says why, and the bookmark can still be saved
    When I fill the add form with the URL "http://127.0.0.1:1/page" and the title ""
    Then the add form says "Could not read the page: Address is not public"
    And the "title" field of the add form shows "Leave blank to use the domain" as its placeholder
    When I add the bookmark "http://127.0.0.1:1/page" titled "" tagged ""
    Then the list shows "127.0.0.1"

  Scenario: The edit form shows what the page said as placeholders and keeps the bookmark's own text
    Given I have the bookmark "https://example.test/read" titled "Mine" whose page says "Their title" and "What the page says about itself"
    When I open the site
    And I start editing "Mine"
    Then the "title" field of the edit form shows "Their title" as its placeholder
    And the "note" field of the edit form shows "What the page says about itself" as its placeholder
    And the edit form holds "Mine" as the title
    And the "note" field of the edit form holds ""

  Scenario: A title and a note left blank are saved as what the page said
    Given I have the bookmark "https://example.test/read" titled "Mine" whose page says "Their title" and "What the page says about itself"
    When I open the site
    And I start editing "Mine"
    And I clear the "title" field of the edit form
    And I save the edit
    Then I am told "Bookmark saved."
    And the list shows "Their title"
    And the list shows "What the page says about itself"
    And the list does not show "Mine"

  Scenario: A title that is typed is saved as typed, and the page's details stay for later
    Given I have the bookmark "https://example.test/read" titled "Mine" whose page says "Their title" and "What the page says about itself"
    When I open the site
    And I start editing "Mine"
    And I type "Renamed" in the "title" field of the edit form
    And I save the edit
    Then I am told "Bookmark saved."
    And the list shows "Renamed"
    And the list does not show "Their title"
    When I start editing "Renamed"
    Then the "title" field of the edit form shows "Their title" as its placeholder

  Scenario: Reading the page again that cannot be read says why and keeps the bookmark's details
    Given I have the bookmark "https://example.test/read" titled "Mine" whose page says "Their title" and "What the page says about itself"
    When I open the site
    And I start editing "Mine"
    Then the read again button of the edit form is no wider than its text
    When I read the page again in the edit form
    Then the edit form says "Could not read the page"
    And the "title" field of the edit form shows "Their title" as its placeholder
    And the "note" field of the edit form shows "What the page says about itself" as its placeholder

  Scenario: Reading the page again in the add form tries the address once more
    When I fill the add form with the URL "http://127.0.0.1:1/page" and the title ""
    Then the add form says "Could not read the page: Address is not public"
    And the page has asked the server to read an address 1 time
    And the read again button of the add form is no wider than its text
    When I read the page again in the add form
    Then the page has asked the server to read an address 2 times
    And the add form says "Could not read the page: Address is not public"

  Scenario: Reading the pages of a selection says how many could not be read
    Given I have the bookmark "http://127.0.0.1:1/page" titled "Private"
    When I open the site
    And I enter select mode
    And I tick "Private"
    And the server answers requests for "/api/bookmarks/read" slowly
    And I read the pages of the selection
    Then the link check shows "Reading pages: 0 of 1"
    And I am told "Read 0 of 1 page, 1 could not be read"
    And "Private" is ticked

  Scenario: Changing the address puts the old page's details away
    Given I have the bookmark "https://example.test/read" titled "Mine" whose page says "Their title" and "What the page says about itself"
    When I open the site
    And I start editing "Mine"
    And I type "http://127.0.0.1:1/elsewhere" in the "url" field of the edit form
    Then the "title" field of the edit form shows "Leave blank to use the domain" as its placeholder
    And the "note" field of the edit form has no placeholder
