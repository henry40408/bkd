Feature: A session that ends while the page is open

  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "https://a.test" titled "Alpha"

  Scenario: The next call returns to sign-in with one message and no toast
    When I open the site
    Then the list shows "Alpha"
    When my session cookie disappears
    And I search for "alpha"
    Then I see the sign-in page
    And the sign-in error says exactly "Your session ended. Sign in again."
    And no toast is shown

  Scenario: Saving after the session ended also returns to sign-in
    When I open the site
    Then the list shows "Alpha"
    When my session cookie disappears
    And I add the bookmark "https://b.test" titled "Beta" tagged "x"
    Then I see the sign-in page
    And the sign-in error says exactly "Your session ended. Sign in again."
    And no toast is shown
