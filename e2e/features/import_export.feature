Feature: Importing and exporting bookmarks

  Background:
    Given I am signed in as the admin "root"

  Scenario: Importing a browser file turns folders into tags
    When I open Import and export
    And I import a bookmarks file exported from a browser
    Then the import result says "Added 2, skipped 0 you already had"
    And the import result says "ignored 1 that are not web links"
    When I choose the tag "recipes"
    Then the list shows "Tomato soup"
    And the list shows "Fresh bread"

  Scenario: A link I already saved is skipped
    Given I have the bookmark "https://soup.test/" titled "My soup"
    When I open Import and export
    And I import a bookmarks file exported from a browser
    Then the import result says "Added 1, skipped 1 you already had"
    And the account has exactly 2 bookmarks

  Scenario: The browser asks for a file before importing
    When I open Import and export
    And I import without choosing a file
    Then the browser refuses the field ".import-form input[type=file]"
    And the address is "/transfer"

  Scenario: The server explains what to do when no file arrives
    When I open Import and export
    And the browser's own form checks are switched off
    And I import without choosing a file
    Then I see the error "Choose a file first."

  Scenario: Reading my own export back adds nothing
    Given I have 3 bookmarks
    Then importing my own export adds nothing new

  Scenario: The export links download both formats
    When I open Import and export
    Then the download links point to the HTML and JSON exports
