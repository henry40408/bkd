Feature: API documentation for programs that speak linkding

  Scenario: The documentation needs no sign-in and names the linkding version it follows
    When I open the site at "/api/docs"
    Then the API documentation lists "Save a bookmark"
    And the API documentation lists "linkding-v"

  Scenario: Try it out sends the token and saves a bookmark
    Given I am signed in as the admin "root"
    When I open Settings
    And I create the API token "Docs"
    And I open the site at "/api/docs"
    And I authorize the API documentation with the new token
    And I try out "createBookmark" with the body '{"url": "https://example.com/from-docs", "title": "From the docs"}'
    Then the API documentation answers "201" for "createBookmark"
    When I open the site
    Then the list shows "From the docs"
