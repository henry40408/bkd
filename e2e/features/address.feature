Feature: The address bar keeps the view

  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "https://a.test/1" titled "Zebra" tagged "stripes"
    And I have the bookmark "https://a.test/2" titled "Other" tagged "plain"

  Scenario: A search survives a reload
    When I open the site
    And I search for "zebra"
    Then the address is "/?q=zebra"
    When I reload the page
    Then the search box holds "zebra"
    And the list shows "Zebra"
    And the list does not show "Other"

  Scenario: Choosing a tag is a step that Back undoes
    When I open the site
    And I choose the tag "stripes"
    Then the address is "/?tag=stripes"
    And the list does not show "Other"
    When I go back
    Then the address is "/"
    And the list shows "Other"

  Scenario: A link opens straight into a filter, with a search
    When I open the site at "/?tag=plain&q=oth"
    Then the list shows "Other"
    And the list does not show "Zebra"
    And the search box holds "oth"

  Scenario: An unknown tag shows an empty list, not an error
    When I open the site at "/?tag=ghost&status=weird"
    Then I see the empty message "No bookmarks match."

  Scenario: A page that does not exist says so
    When I open the site at "/nowhere"
    Then I see the error "does not exist"

  Scenario: Searching inside a tag keeps the tag, and Back returns to the tag
    When I open the site
    And I choose the tag "stripes"
    And I search for "zeb"
    Then the address is "/?q=zeb&tag=stripes"
    When I go back
    Then the address is "/?tag=stripes"
    And the search box holds ""
    And the list shows "Zebra"

  Scenario: Clicking the bkd brand shows the default list without reloading
    When I open the site
    And I choose the tag "stripes"
    And I search for "zeb"
    Then the address is "/?q=zeb&tag=stripes"
    When I mark the page
    And I click the bkd brand
    Then the address is "/"
    And the page was not reloaded
    And the search box holds ""
    And the list shows "Other"

  Scenario: Clicking the bkd brand leaves the Account page
    When I open the site
    And I open Account
    Then the address is "/account"
    When I mark the page
    And I click the bkd brand
    Then the address is "/"
    And the page was not reloaded
    And the list shows "Zebra"

  Scenario: Every page has its own address that survives a reload
    When I open the site
    And I open Account
    And I reload the page
    Then the address is "/account"
    And I am signed in as "root"
    And the list of browsers has 1 rows
