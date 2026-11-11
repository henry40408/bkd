Feature: Link check

  # The server only contacts public addresses, so these scenarios use a private one.
  # What the check finds on real answers (ok, redirected, broken) is covered by the
  # server's own tests, which can run a web server on the same machine.
  Background:
    Given I am signed in as the admin "root"
    And I have the bookmark "http://127.0.0.1:1/page" titled "Private"
    And I have the bookmark "https://example.test/fine" titled "Other"

  Scenario: Checking selected links shows what could not be confirmed
    When I open the site
    And I enter select mode
    And I tick "Private"
    And I check the links of the selection
    Then I am told "Checked 1 bookmark: 1 not confirmed"
    And select mode is off
    And the list shows "Address is not public"

  Scenario: The result can be searched, and an unchecked bookmark is not "broken"
    When I open the site
    And I enter select mode
    And I tick "Private"
    And I check the links of the selection
    And I search for "status:unknown"
    Then the list shows "Private"
    And the list does not show "Other"
    When I search for "status:unchecked"
    Then the list shows "Other"
    And the list does not show "Private"
    When I search for "-status:broken"
    Then the list shows "Private"
    And the list shows "Other"

  Scenario: Check links needs a selection
    When I open the site
    And I enter select mode
    Then the "Check links" button is disabled
    When I tick "Other"
    Then the "Check links" button is enabled
