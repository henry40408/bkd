Feature: The address bar keeps the view

  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "https://a.test/1" titled "Zebra" tagged "stripes"
    And I have the bookmark "https://a.test/2" titled "Other" tagged "plain"

  Scenario: A search survives a reload
    When I open the site
    And I search for "zebra"
    Then the address hash is "/?q=zebra"
    When I reload the page
    Then the search box holds "zebra"
    And the list shows "Zebra"
    And the list does not show "Other"

  Scenario: Choosing a tag is a step that Back undoes
    When I open the site
    And I choose the tag "stripes"
    Then the address hash is "/?tag=stripes"
    And the list does not show "Other"
    When I go back
    Then the address hash is ""
    And the list shows "Other"

  Scenario: A link opens straight into a filter, with a search
    When I open the site at "/#/?tag=plain&q=oth"
    Then the list shows "Other"
    And the list does not show "Zebra"
    And the search box holds "oth"

  Scenario: An address the page does not understand shows everything
    When I open the site at "/#/nowhere?tag=ghost&status=weird"
    Then the list shows "Zebra"
    And the list shows "Other"
    And the address hash is ""

  Scenario: Typing a search and pressing Back returns to the list before it
    When I open the site
    And I choose the tag "stripes"
    And I search for "zeb"
    Then the address hash is "/?q=zeb&tag=stripes"
    When I go back
    Then the address hash is ""
    And the search box holds ""
    And the list shows "Other"
