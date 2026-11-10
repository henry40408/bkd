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
    And the page behind the drawer is inert
    And the focus is on "#sidebar a"
    When I press Escape
    Then the sidebar is hidden
    And the page is not inert
    And the focus is on "#menu-button"

  Scenario: Choosing a tag closes the drawer
    Given the window is phone-sized
    When I open the site
    And I open the menu
    And I choose the tag "rust"
    Then the sidebar is hidden
    And the page is not inert
    And the list shows "Alpha"

  Scenario: Widening the window with the drawer open leaves nothing inert
    Given the window is phone-sized
    When I open the site
    And I open the menu
    Then the page behind the drawer is inert
    When the window becomes desktop-sized
    Then the page is not inert
    And the sidebar is visible

  Scenario: On a phone the row actions need no hover
    Given the window is phone-sized
    When I open the site
    Then the button "Edit Alpha" is visible without hovering
    And the button "Delete Alpha" is visible without hovering
    And the page does not scroll sideways
