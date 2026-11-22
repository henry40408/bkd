Feature: Page details

  # The server only contacts public addresses, so a scenario cannot serve a page
  # to read. What the refresh finds on a real page (Open Graph, title, description,
  # limits) is covered by the server's own tests, which can run a web server on the
  # same machine. These scenarios use a private address for the failure, and put the
  # stored details in place for what is shown.
  Background:
    Given I am signed in as the admin "root"

  Scenario: A page that cannot be read says why and changes nothing
    Given I have the bookmark "http://127.0.0.1:1/page" titled "Private"
    When I open the site
    And I refresh the page details of "Private"
    Then I am told "could not read the page: Address is not public"
    And the list shows "Private"

  Scenario: The list shows the description the page gave
    Given I have the bookmark "https://example.test/read" titled "Mine" whose page says "Their title" and "What the page says about itself"
    When I open the site
    Then the list shows "What the page says about itself"
    And the list shows "Mine"
    And the list does not show "Their title"

  Scenario: The edit form offers the page's title, and using it changes only the field
    Given I have the bookmark "https://example.test/read" titled "Mine" whose page says "Their title" and "A description"
    When I open the site
    And I start editing "Mine"
    Then the edit form says "Page title: Their title"
    And the edit form holds "Mine" as the title
    When I use the page title
    Then the edit form holds "Their title" as the title
    And the page title button is not offered
    When I save the edit
    Then I am told "Bookmark saved."
    And the list shows "Their title"
    And the list shows "A description"
