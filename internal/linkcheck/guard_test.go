package linkcheck

import (
	"net/netip"
	"net/url"
	"reflect"
	"testing"
)

func TestPrivateAndSpecialAddressesAreNotPublic(t *testing.T) {
	for _, text := range []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "0.0.0.0", "100.64.0.1", "100.127.255.255", "198.18.0.1",
		"192.0.0.8", "192.0.2.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::10.0.0.1", "fc00::1", "fd12::1", "fe80::1", "ff02::1",
		"2001:db8::1", "2002:c0a8:0101::1", "64:ff9b::a00:1", "::ffff:10.0.0.1", "::ffff:127.0.0.1",
	} {
		if isPublic(netip.MustParseAddr(text)) {
			t.Errorf("%s must not be public", text)
		}
	}
}

func TestReservedRangesThatAreEasyToMissAreNotPublic(t *testing.T) {
	for _, text := range []string{
		"0.1.2.3", "192.88.99.1", "::ffff:0:7f00:1", "100::1", "fec0::1", "2001::1", "3fff::1", "5f00::1",
	} {
		if isPublic(netip.MustParseAddr(text)) {
			t.Errorf("%s must not be public", text)
		}
	}
}

func TestOrdinaryAddressesArePublic(t *testing.T) {
	for _, text := range []string{
		"8.8.8.8", "1.1.1.1", "93.184.216.34", "172.32.0.1", "172.15.255.255", "100.63.255.255",
		"100.128.0.1", "198.17.255.255", "198.20.0.1", "2606:4700:4700::1111", "::ffff:8.8.8.8",
	} {
		if !isPublic(netip.MustParseAddr(text)) {
			t.Errorf("%s must be public", text)
		}
	}
}

func TestAURLThatGivesAPrivateAddressOrAForeignSchemeIsRefused(t *testing.T) {
	g := guard{}
	// A host name such as `localhost` passes here. The dialer refuses it later.
	for _, refused := range []string{
		"http://127.0.0.1/", "http://[::1]/", "http://10.0.0.5/x", "http://2130706433/", "http://0x7f.1/",
		"http://0177.0.0.1/", "http://127.1/", "http://127.0.0.1./", "http://169.254.169.254/latest",
		"http://[::ffff:10.0.0.1]/", "ftp://example.test/", "file:///etc/passwd", "http:///nohost",
	} {
		if g.allowsURL(mustParseURL(t, refused)) {
			t.Errorf("%s must be refused", refused)
		}
	}
	for _, allowed := range []string{"https://example.test/", "http://8.8.8.8/", "http://999.example/", "http://1.2.3.4.5/"} {
		if !g.allowsURL(mustParseURL(t, allowed)) {
			t.Errorf("%s must be allowed", allowed)
		}
	}
}

func TestTheAllowHookAdmitsOnlyWhatItNames(t *testing.T) {
	g := guard{allow: netip.Addr.IsLoopback}
	if !g.allowsURL(mustParseURL(t, "http://127.0.0.1:8080/")) {
		t.Error("loopback must pass with the hook")
	}
	if g.allowsURL(mustParseURL(t, "http://10.0.0.1/")) {
		t.Error("the hook must not admit private addresses")
	}
}

func TestLegacyIPv4SpellingsBecomeTheirAddress(t *testing.T) {
	for host, want := range map[string]string{
		"2130706433": "127.0.0.1", "0x7f.1": "127.0.0.1", "0177.0.0.01": "127.0.0.1",
		"127.1": "127.0.0.1", "1.2.3.4.": "1.2.3.4", "0xffffffff": "255.255.255.255",
	} {
		got, ok := parseLegacyIPv4(host)
		if !ok || got.String() != want {
			t.Errorf("%s: got %v %v, want %s", host, got, ok, want)
		}
	}
	for _, host := range []string{"example.com", "256.1.1.1", "1.2.3.4.5", "4294967296", "08.1", "", "1..2"} {
		if _, ok := parseLegacyIPv4(host); ok {
			t.Errorf("%q is not an address", host)
		}
	}
}

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func mustParseURL(t *testing.T, text string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
