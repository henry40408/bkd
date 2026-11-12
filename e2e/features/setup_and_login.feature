Feature: Setup and sign-in

  Scenario: The first admin is created with the setup token from the server log
    When I open the site
    Then I see the setup page
    And the address is "/setup"
    When I create the first admin "root" with the setup token
    Then I am signed in as "root"
    And the address is "/"
    And I can open Manage users

  Scenario: A wrong setup token is refused
    When I open the site
    And I try to create the first admin "root" with the token "AAAAA-AAAAA"
    Then I see the error "invalid setup token"
    And I still see the setup page

  Scenario: Signing in and out
    Given the admin "root" exists
    When I open the site
    Then I see the sign-in page
    And the address is "/login?next=%2F"
    When I sign in as "root" with the right password
    Then I am signed in as "root"
    And the address is "/"
    When I sign out
    Then I see the sign-in page
    And the address is "/login"

  Scenario: A wrong password gets one plain refusal
    Given the admin "root" exists
    When I open the site
    And I sign in as "root" with a wrong password
    Then I see the error "invalid username or password"
    And I still see the sign-in page

  Scenario: Pressing Enter twice while signing in never puts the password in the address
    Given the admin "root" exists
    When I open the site
    And I press Enter twice to sign in as "root"
    Then I am signed in as "root"
    And the address has no query string
    And the browser reports no policy violation

  Scenario: A page opened while signed out returns there after sign-in
    Given the admin "root" exists
    When I open the site at "/account"
    Then I see the sign-in page
    And the address is "/login?next=%2Faccount"
    When I sign in as "root" with the right password
    Then the address is "/account"
