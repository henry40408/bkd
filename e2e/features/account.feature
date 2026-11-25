Feature: Changing my own password

  Background:
    Given I am signed in as the admin "root"

  Scenario: A new password signs out other sessions but not this browser
    When I open Settings
    And I change my password from "correct-horse-battery" to "a-brand-new-password"
    Then I am told "Password changed. Your other browsers were signed out."
    And the address is "/settings?notice=password-changed"
    And the session of "root" no longer works
    And "root" can sign in with the password "a-brand-new-password"
    When I reload the page
    Then I am signed in as "root"

  Scenario: A wrong current password changes nothing
    When I open Settings
    And I change my password from "not-the-password" to "a-brand-new-password"
    Then I see the error "current password is wrong"
    And "root" can sign in with the temporary password

  Scenario: The browser stops a new password that is too short
    When I open Settings
    And I change my password from "correct-horse-battery" to "short"
    Then the browser refuses the field "form[action$='/password'] input[name=new]"
    And the address is "/settings"
    And "root" can sign in with the temporary password

  # Password managers may fill these fields without firing the events a script
  # listens for, so the button must never be disabled.
  Scenario: The password button is never disabled
    When I open Settings
    And I fill in the password form from "correct-horse-battery" to "short"
    Then the password form can be saved

  Scenario: The server also refuses a new password that is too short
    When I open Settings
    And the browser's own form checks are switched off
    And I change my password from "correct-horse-battery" to "short"
    Then I see the error "at least 12 characters"
    And "root" can sign in with the temporary password

  Scenario: Two different new passwords are refused
    When I open Settings
    And I change my password from "correct-horse-battery" to "a-brand-new-password" but repeat "a-brand-new-passwore"
    Then I see the error "are different"
    And "root" can sign in with the temporary password

  Scenario: The list shows this browser and ends another one
    Given I am also signed in on another browser
    When I open Settings
    Then the list of browsers has 2 rows
    When I sign out the other browser
    Then I am told "That browser was signed out."
    And the list of browsers has 1 rows
    And the session of "elsewhere" no longer works

  Scenario: Signing out all other browsers keeps this one
    Given I am also signed in on another browser
    When I open Settings
    And I sign out all other browsers
    Then I am told "All other browsers were signed out."
    And the list of browsers has 1 rows
    And the session of "elsewhere" no longer works
    And I am signed in as "root"

  Scenario: Reloading the Settings page keeps the list of browsers
    Given I am also signed in on another browser
    When I open Settings
    And I reload the page
    Then the list of browsers has 2 rows

  Scenario: The old address of the Account page leads to Settings
    When I open the site at "/account"
    Then the address is "/settings"
    And the heading says "Settings"

  Scenario: The sections of the page are set apart by one heavy rule and light ones
    When I open Settings
    Then the rules above the sections are "2px 1px 1px 1px 1px"
    And the Save button is clear of the rule below it
