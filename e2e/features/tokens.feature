Feature: API tokens for programs that cannot sign in through the browser

  Background:
    Given I am signed in as the admin "root"

  Scenario: A new token is shown once and then only listed by name
    When I open Settings
    And I create the API token "Browser extension"
    Then I am shown the new token once
    And the token list shows "Browser extension"
    When I reload the page
    Then the token list shows "Browser extension"
    And the page no longer shows the new token

  Scenario: The new token is copied with one click
    When I open Settings
    And I create the API token "Browser extension"
    And I copy the new token
    Then the Copy button says "Copied"
    And the clipboard holds the new token

  Scenario: The new token is still copied when the clipboard API refuses
    Given the browser refuses writes through the clipboard API
    When I open Settings
    And I create the API token "Browser extension"
    And I copy the new token
    Then the Copy button says "Copied"
    And the clipboard holds the new token

  Scenario: Without the script the token stays selectable and there is no Copy button
    Given scripts are switched off
    When I open Settings
    And I create the API token "Browser extension"
    Then I am shown the new token once
    And the Copy button is not shown

  Scenario: Revoking a token removes it from the list
    When I open Settings
    And I create the API token "Old script"
    And I create the API token "Browser extension"
    And I revoke the API token "Old script"
    Then I am told "That token was ended."
    And the token list shows "Browser extension"
    And the token list does not show "Old script"
    And the address is "/settings?notice=token-revoked"

  Scenario: A token needs a name
    When I open Settings
    And the browser's own form checks are switched off
    And I try to create the API token "   "
    Then I see the error "name is required"
