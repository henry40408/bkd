Feature: Managing users

  Background:
    Given I am signed in as the admin "root"

  Scenario: An admin creates a member who can sign in
    When I open Manage users
    And I create the user "mei" with a temporary password
    Then the user list shows "mei"
    And "mei" can sign in with the temporary password

  Scenario: A member cannot see the user management
    Given the member "mei" exists
    When I sign in as "mei" in the browser
    Then I am signed in as "mei"
    And I cannot open Manage users

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
    Then the session of "mei" no longer works
    And "mei" can sign in with the password "a-brand-new-password"

  Scenario: An admin cannot demote, disable, delete or reset themselves
    When I open Manage users
    Then the row of "root" has no Demote, Disable, Delete or Reset password button

  Scenario: Deleting a member
    Given the member "mei" exists
    When I open Manage users
    And I delete "mei"
    Then the user list does not show "mei"
