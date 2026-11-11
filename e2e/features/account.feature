Feature: Changing my own password

  Background:
    Given I am signed in as the admin "root"

  Scenario: A new password signs out other sessions but not this browser
    When I open Change password
    And I change my password from "correct-horse-battery" to "a-brand-new-password"
    Then the password form is cleared
    And the session of "root" no longer works
    And "root" can sign in with the password "a-brand-new-password"
    When I reload the page
    Then I am signed in as "root"

  Scenario: A wrong current password changes nothing
    When I open Change password
    And I change my password from "not-the-password" to "a-brand-new-password"
    Then I see the error "current password is wrong"
    And "root" can sign in with the temporary password

  Scenario: A new password that is too short is refused
    When I open Change password
    And I change my password from "correct-horse-battery" to "short"
    Then I see the error "at least 12 characters"

  Scenario: Two different new passwords are refused before anything is sent
    When I open Change password
    And I change my password from "correct-horse-battery" to "a-brand-new-password" but repeat "a-brand-new-passwore"
    Then I see the error "do not match"
    And "root" can sign in with the temporary password
