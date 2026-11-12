Feature: A session that ends while the page is open

  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "https://a.test" titled "Alpha"

  Scenario: The next page returns to sign-in with one message, then back to where I was
    When I open the site
    Then the list shows "Alpha"
    When my session stops working
    And I search for "alpha"
    Then I see the sign-in page
    And the sign-in page says exactly "Your session ended. Sign in again."
    And the address is "/login?next=%2F%3Fq%3Dalpha&notice=session-ended"
    When I sign in as "root" with the right password
    Then the address is "/?q=alpha"
    And the search box holds "alpha"
    And the list shows "Alpha"

  Scenario: Saving after the session ended returns to sign-in and saves nothing
    When I open the site
    Then the list shows "Alpha"
    When my session stops working
    And I add the bookmark "https://b.test" titled "Beta" tagged "x"
    Then I see the sign-in page
    And the sign-in page says exactly "Your session ended. Sign in again."
    When I sign in as "root" with the right password
    Then the address is "/"
    And the account has exactly 1 bookmark
