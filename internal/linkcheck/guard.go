// Package linkcheck asks a bookmark's address whether it still answers.
//
// The server makes the request, so a user could aim it at the server's own
// network. Two rules prevent that. Only public addresses are contacted, whether
// the URL names one directly or a host name resolves to one, and every redirect
// is held to the same rule. The dialer filters the addresses the connection
// then uses, so the check cannot be undone by a second DNS answer.
package linkcheck

import (
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// errBlocked means every address of a host is off limits.
var errBlocked = errors.New("the address is not public")

// guard decides which addresses may be contacted.
type guard struct {
	// allow admits addresses that are not public. Only tests set it, so they
	// can use a server on this machine.
	allow func(netip.Addr) bool
}

func (g guard) allows(addr netip.Addr) bool {
	addr = addr.Unmap()
	return isPublic(addr) || (g.allow != nil && g.allow(addr))
}

// allowsURL checks the scheme, and the host when the URL gives an IP address
// instead of a name. A name is checked later, by the dialer.
func (g guard) allowsURL(u *url.URL) bool {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	addr, isAddress := parseHostAddress(host)
	if !isAddress {
		return true
	}
	return g.allows(addr)
}

// parseHostAddress reads the address spellings a URL parser accepts and a
// browser would connect to, such as `2130706433` and `0x7f.1`.
func parseHostAddress(host string) (netip.Addr, bool) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr, true
	}
	return parseLegacyIPv4(host)
}

// parseLegacyIPv4 follows the WHATWG URL host rules: one to four numbers in
// decimal, octal (leading 0) or hex (leading 0x), where the last one fills the
// remaining bytes.
func parseLegacyIPv4(host string) (netip.Addr, bool) {
	parts := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(parts) > 4 {
		return netip.Addr{}, false
	}
	numbers := make([]uint64, len(parts))
	for i, part := range parts {
		number, ok := parseLegacyNumber(part)
		if !ok {
			return netip.Addr{}, false
		}
		numbers[i] = number
	}
	last := numbers[len(numbers)-1]
	if last >= 1<<(8*(5-len(numbers))) {
		return netip.Addr{}, false
	}
	value := last
	for i, number := range numbers[:len(numbers)-1] {
		if number > 255 {
			return netip.Addr{}, false
		}
		value += number << (8 * (3 - i))
	}
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}), true
}

func parseLegacyNumber(part string) (uint64, bool) {
	if part == "" {
		return 0, false
	}
	base, digits := 10, part
	switch {
	case len(part) > 1 && (strings.HasPrefix(part, "0x") || strings.HasPrefix(part, "0X")):
		base, digits = 16, part[2:]
	case len(part) > 1 && part[0] == '0':
		base, digits = 8, part[1:]
	}
	if digits == "" {
		return 0, true
	}
	number, err := strconv.ParseUint(digits, base, 32)
	return number, err == nil
}

// isPublic reports whether an address belongs to the public internet.
func isPublic(addr netip.Addr) bool {
	// `::ffff:10.0.0.1` reaches 10.0.0.1, so it is judged as that address.
	addr = addr.Unmap()
	if addr.Is4() {
		return isPublicV4(addr)
	}
	return addr.Is6() && isPublicV6(addr)
}

func isPublicV4(addr netip.Addr) bool {
	octets := addr.As4()
	a, b, c := octets[0], octets[1], octets[2]
	documentation := (a == 192 && b == 0 && c == 2) ||
		(a == 198 && b == 51 && c == 100) ||
		(a == 203 && b == 0 && c == 113)
	sharedAddressSpace := a == 100 && b >= 64 && b < 128
	protocolAssignments := a == 192 && b == 0 && c == 0
	benchmarking := a == 198 && (b == 18 || b == 19)
	relayAnycast := a == 192 && b == 88 && c == 99
	reserved := a >= 240
	thisNetwork := a == 0
	return !(addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsMulticast() ||
		documentation ||
		sharedAddressSpace ||
		protocolAssignments ||
		benchmarking ||
		relayAnycast ||
		reserved ||
		thisNetwork)
}

func isPublicV6(addr netip.Addr) bool {
	bytes := addr.As16()
	first := uint16(bytes[0])<<8 | uint16(bytes[1])
	second := uint16(bytes[2])<<8 | uint16(bytes[3])
	// Only 2000::/3 is global unicast. Everything else, assigned or not, is
	// refused, which covers `::`, `::1`, unique-local, link-local, site-local,
	// multicast, NAT64 and the IPv4-compatible forms.
	globalUnicast := first&0xe000 == 0x2000
	teredoAndSpecial := first == 0x2001 && second < 0x0200
	documentation := (first == 0x2001 && second == 0x0db8) || (first == 0x3fff && second < 0x1000)
	// This one embeds an IPv4 address that a gateway may forward to.
	sixToFour := first == 0x2002
	return globalUnicast && !(teredoAndSpecial || documentation || sixToFour)
}
