Feature: Narrow windows

  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "https://a.test" titled "Alpha" tagged "rust"

  Scenario: A wide window shows the sidebar and no menu button
    When I open the site
    Then the sidebar is visible
    And the menu button is hidden

  Scenario: On a phone the sidebar is a drawer
    Given the window is phone-sized
    When I open the site
    Then the sidebar is hidden
    And the menu button is visible
    When I open the menu
    Then the sidebar is visible
    And the backdrop covers the page
    When I press Escape
    Then the sidebar is hidden
    And the backdrop is hidden

  Scenario: Tapping the backdrop closes the drawer
    Given the window is phone-sized
    When I open the site
    And I open the menu
    Then the sidebar is visible
    When I tap the backdrop
    Then the sidebar is hidden

  Scenario: Choosing a tag closes the drawer
    Given the window is phone-sized
    When I open the site
    And I open the menu
    And I choose the tag "rust"
    Then the address is "/?tag=rust"
    And the sidebar is hidden
    And the list shows "Alpha"

  Scenario: Widening the window with the drawer open shows the sidebar and no backdrop
    Given the window is phone-sized
    When I open the site
    And I open the menu
    Then the backdrop covers the page
    When the window becomes desktop-sized
    Then the backdrop is hidden
    And the sidebar is visible

  Scenario: On a phone the row actions need no hover
    Given the window is phone-sized
    When I open the site
    Then the link "Edit Alpha" is visible without hovering
    And the link "Delete Alpha" is visible without hovering
    And the page does not scroll sideways

  Scenario: The drawer opens without a script
    Given the window is phone-sized
    And scripts are switched off
    When I open the site
    Then the sidebar is hidden
    When I open the menu
    Then the sidebar is visible
    And the backdrop covers the page
    When I tap the backdrop
    Then the sidebar is hidden
