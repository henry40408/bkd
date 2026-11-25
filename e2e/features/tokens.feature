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
