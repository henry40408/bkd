Feature: Importing and exporting bookmarks

  Background:
    Given I am signed in as the admin "root"

  Scenario: Importing a browser file turns folders into tags
    When I open Import and export
    And I import a bookmarks file exported from a browser
    Then the import result says "Added 2 bookmarks."
    And the import result says "Skipped 1 that are not web links"
    When I choose the tag "recipes"
    Then the list shows "Tomato soup"
    And the list shows "Fresh bread"

  Scenario: A link I already saved is skipped
    Given I have the bookmark "https://soup.test/" titled "My soup"
    When I open Import and export
    And I import a bookmarks file exported from a browser
    Then the import result says "Added 1 bookmark."
    And the import result says "Skipped 1 you already had."
    And the account has exactly 2 bookmarks

  Scenario: Importing without choosing a file explains what to do
    When I open Import and export
    And I import without choosing a file
    Then I see the error "Choose a bookmarks file first."

  Scenario: Reading my own export back adds nothing
    Given I have 3 bookmarks
    Then importing my own export adds nothing new

  Scenario: The export links download both formats
    When I open Import and export
    Then the download links point to the HTML and JSON exports
