Feature: Changing my own password

  Background:
    Given I am signed in as the admin "root"

  Scenario: A new password signs out other sessions but not this browser
    When I open Account
    And I change my password from "correct-horse-battery" to "a-brand-new-password"
    Then the password form is cleared
    And the session of "root" no longer works
    And "root" can sign in with the password "a-brand-new-password"
    When I reload the page
    Then I am signed in as "root"

  Scenario: A wrong current password changes nothing
    When I open Account
    And I change my password from "not-the-password" to "a-brand-new-password"
    Then I see the error "current password is wrong"
    And "root" can sign in with the temporary password

  Scenario: A new password that is too short is refused
    When I open Account
    And I change my password from "correct-horse-battery" to "short"
    Then I see the error "at least 12 characters"

  Scenario: Two different new passwords are refused before anything is sent
    When I open Account
    And I change my password from "correct-horse-battery" to "a-brand-new-password" but repeat "a-brand-new-passwore"
    Then I see the error "do not match"
    And "root" can sign in with the temporary password

  Scenario: The list shows this browser and ends another one
    Given I am also signed in on another browser
    When I open Account
    Then the list of browsers has 2 rows
    When I sign out the other browser
    Then the list of browsers has 1 rows
    And the session of "elsewhere" no longer works

  Scenario: Signing out all other browsers keeps this one
    Given I am also signed in on another browser
    When I open Account
    And I sign out all other browsers
    Then the list of browsers has 1 rows
    And the session of "elsewhere" no longer works
    And I am signed in as "root"

  Scenario: Reloading the Account page keeps the list of browsers
    Given I am also signed in on another browser
    When I open Account
    And I reload the page
    Then the list of browsers has 2 rows
