package linkcheck

import (
	"strings"

	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/version"
)

// MaxUserAgentLength is the longest User-Agent a user may choose. Real browser
// strings are well under it.
const MaxUserAgentLength = 200

// DefaultUserAgent is honest about being a robot, and says where to ask about it.
// The `Mozilla/5.0 (compatible; ...)` shape is the one crawlers such as Googlebot use.
// It is what the check sends for a user who chose no other.
func DefaultUserAgent() string {
	return "Mozilla/5.0 (compatible; bkd-link-checker/" + version.Version() + "; +https://github.com/henry40408/bkd)"
}

// ValidateUserAgent returns the User-Agent to keep. Empty (after trimming)
// means "use the default". Anything but printable ASCII is refused: the value
// goes into a request header, where a line break would start another header.
func ValidateUserAgent(input string) (string, error) {
	agent := strings.TrimSpace(input)
	if len(agent) > MaxUserAgentLength {
		return "", bookmarks.ValidationError("user agent is too long")
	}
	for i := range len(agent) {
		if agent[i] < ' ' || agent[i] > '~' {
			return "", bookmarks.ValidationError("user agent must use printable ASCII characters only")
		}
	}
	return agent, nil
}
