Feature: Setup and sign-in

  Scenario: The first admin is created with the setup token from the server log
    When I open the site
    Then I see the setup page
    When I create the first admin "root" with the setup token
    Then I am signed in as "root"
    And I can open Manage users

  Scenario: A wrong setup token is refused
    When I open the site
    And I try to create the first admin "root" with the token "AAAAA-AAAAA"
    Then I still see the setup page
    And I see the error "invalid setup token"

  Scenario: Signing in and out
    Given the admin "root" exists
    When I open the site
    Then I see the sign-in page
    When I sign in as "root" with the right password
    Then I am signed in as "root"
    When I sign out
    Then I see the sign-in page

  Scenario: A wrong password gets one plain refusal
    Given the admin "root" exists
    When I open the site
    And I sign in as "root" with a wrong password
    Then I still see the sign-in page
    And I see the error "invalid username or password"

  Scenario: Pressing Enter twice while signing in never puts the password in the address
    Given the admin "root" exists
    When I open the site
    And I press Enter twice to sign in as "root"
    Then I am signed in as "root"
    And the address has no query string
    And the browser reports no policy violation
