package auth

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

// Forward authentication: a reverse proxy (Authelia, Authentik, oauth2-proxy)
// signs the user in and tells bkd who it is in a request header.
//
// A header is only a claim. It is believed only when the TCP peer is one of the
// proxies the operator listed; from anyone else it is ignored, so a client that
// reaches bkd directly cannot sign in as somebody by typing a header. The proxy
// must in turn overwrite the header on every request it forwards.

// ForwardAuthConfig is what the operator configured.
type ForwardAuthConfig struct {
	// TrustedProxies are addresses or CIDR ranges, such as 127.0.0.1 or 172.18.0.0/16.
	TrustedProxies []string
	UserHeader     string
	GroupsHeader   string
	// AdminGroup members are admins. Without it, bkd never promotes or demotes anyone.
	AdminGroup string
	// Logger may be nil.
	Logger *slog.Logger
}

type ForwardAuth struct {
	trusted      []netip.Prefix
	userHeader   string
	groupsHeader string
	adminGroup   string
	logger       *slog.Logger
}

// IdentityKind says what the proxy told bkd about the caller.
type IdentityKind int

const (
	// IdentityAbsent: not from a trusted proxy, or no user header. Use the session cookie.
	IdentityAbsent IdentityKind = iota
	// IdentityInvalid: a trusted proxy sent a name that cannot be a bkd username.
	// Nobody is signed in.
	IdentityInvalid
	IdentityUser
)

// Identity is the result of ForwardAuth.Identify.
type Identity struct {
	Kind     IdentityKind
	Username string
	// IsAdmin is non-nil when an admin group is configured: whether the user is in it.
	IsAdmin *bool
}

func NewForwardAuth(config ForwardAuthConfig) (*ForwardAuth, error) {
	if len(config.TrustedProxies) == 0 {
		return nil, errors.New("forward auth needs at least one trusted proxy address")
	}
	trusted := make([]netip.Prefix, 0, len(config.TrustedProxies))
	for _, text := range config.TrustedProxies {
		network, err := ParseNetwork(text)
		if err != nil {
			return nil, err
		}
		trusted = append(trusted, network)
	}
	for _, name := range []string{config.UserHeader, config.GroupsHeader} {
		if !isHeaderName(name) {
			return nil, fmt.Errorf("%q is not a header name", name)
		}
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &ForwardAuth{
		trusted:      trusted,
		userHeader:   config.UserHeader,
		groupsHeader: config.GroupsHeader,
		adminGroup:   config.AdminGroup,
		logger:       logger,
	}, nil
}

// Identify reads the user header of a request whose TCP peer is peer. An
// invalid peer address means the peer is unknown.
func (forward *ForwardAuth) Identify(peer netip.Addr, headers http.Header) Identity {
	if !forward.isTrusted(peer) {
		return Identity{Kind: IdentityAbsent}
	}
	values := headers.Values(forward.userHeader)
	if len(values) == 0 {
		return Identity{Kind: IdentityAbsent}
	}
	// Two values mean the proxy appended to a header the client sent, so the first
	// one is the client's. A value that is not plain text is no name either. In both
	// cases nobody is signed in, and the cookie must not fill the gap.
	if !isVisibleASCII(values[0]) {
		return Identity{Kind: IdentityInvalid}
	}
	if len(values) > 1 {
		forward.logger.Warn("more than one forwarded user header", "peer", peer)
		return Identity{Kind: IdentityInvalid}
	}
	if values[0] == "" {
		return Identity{Kind: IdentityAbsent}
	}
	username, err := NormalizeUsername(values[0])
	if err != nil {
		forward.logger.Warn("forwarded user name is not a valid bkd username", "peer", peer)
		return Identity{Kind: IdentityInvalid}
	}
	return Identity{Kind: IdentityUser, Username: username, IsAdmin: forward.adminDecision(headers)}
}

func (forward *ForwardAuth) adminDecision(headers http.Header) *bool {
	if forward.adminGroup == "" {
		return nil
	}
	// Authelia sends Remote-Groups as a comma-separated list.
	groups := headers.Get(forward.groupsHeader)
	inGroup := false
	if isVisibleASCII(groups) {
		for _, name := range strings.Split(groups, ",") {
			inGroup = inGroup || strings.TrimSpace(name) == forward.adminGroup
		}
	}
	return &inGroup
}

func (forward *ForwardAuth) isTrusted(peer netip.Addr) bool {
	if !peer.IsValid() {
		return false
	}
	peer = peer.Unmap().WithZone("")
	for _, network := range forward.trusted {
		if network.Contains(peer) {
			return true
		}
	}
	return false
}

// ParseNetwork reads one address or a range, as in 10.0.0.0/8.
func ParseNetwork(text string) (netip.Prefix, error) {
	addressText, prefixText, hasPrefix := strings.Cut(text, "/")
	address, err := netip.ParseAddr(strings.TrimSpace(addressText))
	if err != nil || address.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q is not an IP address or CIDR range", text)
	}
	address = address.Unmap()
	longest := address.BitLen()
	if !hasPrefix {
		return netip.PrefixFrom(address, longest), nil
	}
	bits, err := strconv.ParseUint(strings.TrimSpace(prefixText), 10, 8)
	if err != nil || int(bits) > longest {
		return netip.Prefix{}, fmt.Errorf("%q has a bad prefix length", text)
	}
	return netip.PrefixFrom(address, int(bits)), nil
}

// isVisibleASCII matches what a header value must hold to count as text: space
// to tilde, and tab.
func isVisibleASCII(value string) bool {
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char != '\t' && (char < ' ' || char > '~') {
			return false
		}
	}
	return true
}

// isHeaderName accepts an HTTP token: letters, digits and a few symbols.
func isHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		char := name[index]
		isLetterOrDigit := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9')
		if !isLetterOrDigit && !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(char)) {
			return false
		}
	}
	return true
}
