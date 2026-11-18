package auth

import (
	"net/http"
	"net/netip"
	"testing"
)

func newForwardAuth(t *testing.T, adminGroup string) *ForwardAuth {
	t.Helper()
	forward, err := NewForwardAuth(ForwardAuthConfig{
		TrustedProxies: []string{"127.0.0.1", "172.18.0.0/16", "fd00::/8"},
		UserHeader:     "Remote-User",
		GroupsHeader:   "Remote-Groups",
		AdminGroup:     adminGroup,
	})
	if err != nil {
		t.Fatal(err)
	}
	return forward
}

func forwardHeaders(user, groups string) http.Header {
	headers := http.Header{}
	headers.Set("Remote-User", user)
	headers.Set("Remote-Groups", groups)
	return headers
}

func peerAddr(text string) netip.Addr { return netip.MustParseAddr(text) }

func TestATrustedProxyNamesTheUser(t *testing.T) {
	found := newForwardAuth(t, "").Identify(peerAddr("172.18.0.5"), forwardHeaders("Henry", ""))
	if found.Kind != IdentityUser || found.Username != "henry" || found.IsAdmin != nil {
		t.Fatalf("got %+v", found)
	}
}

func TestAnyoneElseIsIgnored(t *testing.T) {
	forward := newForwardAuth(t, "")
	for _, peer := range []string{"172.19.0.1", "8.8.8.8", "::1", "fe80::1"} {
		found := forward.Identify(peerAddr(peer), forwardHeaders("henry", ""))
		if found.Kind != IdentityAbsent {
			t.Errorf("%s: %+v", peer, found)
		}
	}
	if found := forward.Identify(netip.Addr{}, forwardHeaders("henry", "")); found.Kind != IdentityAbsent {
		t.Errorf("unknown peer: %+v", found)
	}
}

func TestAnIPv4PeerMappedIntoIPv6StillMatches(t *testing.T) {
	found := newForwardAuth(t, "").Identify(peerAddr("::ffff:127.0.0.1"), forwardHeaders("henry", ""))
	if found.Kind != IdentityUser {
		t.Fatalf("got %+v", found)
	}
}

func TestIPv6RangesMatch(t *testing.T) {
	found := newForwardAuth(t, "").Identify(peerAddr("fd12::1"), forwardHeaders("henry", ""))
	if found.Kind != IdentityUser {
		t.Fatalf("got %+v", found)
	}
}

func TestAMissingOrEmptyHeaderFallsBackToTheCookie(t *testing.T) {
	forward := newForwardAuth(t, "")
	if found := forward.Identify(peerAddr("127.0.0.1"), http.Header{}); found.Kind != IdentityAbsent {
		t.Errorf("missing: %+v", found)
	}
	if found := forward.Identify(peerAddr("127.0.0.1"), forwardHeaders("", "")); found.Kind != IdentityAbsent {
		t.Errorf("empty: %+v", found)
	}
}

func TestARepeatedOrNonTextHeaderSignsNobodyIn(t *testing.T) {
	forward := newForwardAuth(t, "")
	twice := forwardHeaders("mei", "")
	twice.Add("Remote-User", "root")
	if found := forward.Identify(peerAddr("127.0.0.1"), twice); found.Kind != IdentityInvalid {
		t.Errorf("repeated: %+v", found)
	}

	accented := http.Header{"Remote-User": {"josé"}}
	if found := forward.Identify(peerAddr("127.0.0.1"), accented); found.Kind != IdentityInvalid {
		t.Errorf("accented: %+v", found)
	}
}

func TestANameBkdCannotHoldSignsNobodyIn(t *testing.T) {
	forward := newForwardAuth(t, "")
	for _, name := range []string{"ab", "has space", "-lead", "a@b.test"} {
		found := forward.Identify(peerAddr("127.0.0.1"), forwardHeaders(name, ""))
		if found.Kind != IdentityInvalid {
			t.Errorf("%q: %+v", name, found)
		}
	}
}

func TestTheAdminGroupDecidesTheRole(t *testing.T) {
	forward := newForwardAuth(t, "admins")
	admin := forward.Identify(peerAddr("127.0.0.1"), forwardHeaders("henry", "dev, admins"))
	member := forward.Identify(peerAddr("127.0.0.1"), forwardHeaders("henry", "dev,administrators"))
	if admin.IsAdmin == nil || !*admin.IsAdmin {
		t.Errorf("admin: %+v", admin)
	}
	if member.IsAdmin == nil || *member.IsAdmin {
		t.Errorf("member: %+v", member)
	}
}

func TestABadSettingIsRefusedAtStartup(t *testing.T) {
	for _, proxies := range [][]string{{}, {"not-an-ip"}, {"10.0.0.0/33"}, {"fe80::1%eth0"}} {
		_, err := NewForwardAuth(ForwardAuthConfig{
			TrustedProxies: proxies,
			UserHeader:     "Remote-User",
			GroupsHeader:   "Remote-Groups",
		})
		if err == nil {
			t.Errorf("%v was accepted", proxies)
		}
	}
	_, err := NewForwardAuth(ForwardAuthConfig{
		TrustedProxies: []string{"127.0.0.1"},
		UserHeader:     "bad header",
		GroupsHeader:   "Remote-Groups",
	})
	if err == nil {
		t.Error("a header name with a space was accepted")
	}
}

func TestAZeroLengthPrefixMatchesEverything(t *testing.T) {
	network, err := ParseNetwork("0.0.0.0/0")
	if err != nil {
		t.Fatal(err)
	}
	if !network.Contains(peerAddr("203.0.113.9")) {
		t.Fatal("0.0.0.0/0 does not contain 203.0.113.9")
	}
}
