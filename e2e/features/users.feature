Feature: Managing users

  Background:
    Given I am signed in as the admin "root"

  Scenario: An admin creates a member who can sign in
    When I open Manage users
    And I create the user "mei" with a temporary password
    Then I am told "Saved."
    And the user list shows "mei"
    And "mei" can sign in with the temporary password

  Scenario: A member cannot see the user management
    Given the member "mei" exists
    When I sign in as "mei" in the browser
    Then I am signed in as "mei"
    And I cannot open Manage users

  Scenario: A member who opens the user page is refused
    Given the member "mei" exists
    When I sign in as "mei" in the browser
    And I open the site at "/users"
    Then I see the error "admin"

  Scenario: Disabling a member ends their session at once
    Given the member "mei" exists and is signed in elsewhere
    When I open Manage users
    And I disable "mei"
    Then the user list shows "mei" as disabled
    And the session of "mei" no longer works

  Scenario: Resetting a password signs the member out and changes the login
    Given the member "mei" exists and is signed in elsewhere
    When I open Manage users
    And I reset the password of "mei" to "a-brand-new-password"
    Then I am told "Saved."
    And the address is "/users?notice=user-changed"
    And the session of "mei" no longer works
    And "mei" can sign in with the password "a-brand-new-password"

  Scenario: An admin cannot demote, disable, delete or reset themselves
    When I open Manage users
    Then the row of "root" has no Demote, Disable, Delete or Reset password control

  Scenario: Deleting a member asks first
    Given the member "mei" exists
    When I open Manage users
    And I start deleting the user "mei"
    Then I am asked "Delete mei?"
    When I keep it
    Then the address is "/users"
    And the user list shows "mei"
    When I delete "mei"
    Then the user list does not show "mei"

  Scenario: An old confirmation sends the admin to confirm the password first
    Given the member "mei" exists
    And my password confirmation is old
    When I open Manage users
    And I disable "mei"
    Then I am asked to confirm my password
    And the address is "/confirm"
    When I confirm my password with "not-the-password"
    Then I see the error "password is wrong"
    When I confirm my password with "correct-horse-battery"
    Then the address is "/users?notice=confirmed"
    And I am told "Password confirmed."
    And "mei" can sign in with the temporary password
    When I disable "mei"
    Then the user list shows "mei" as disabled

  Scenario: Cancelling the confirmation leaves the account alone
    Given the member "mei" exists
    And my password confirmation is old
    When I open Manage users
    And I disable "mei"
    And I cancel the confirmation
    Then the address is "/users"
    And "mei" can sign in with the temporary password
