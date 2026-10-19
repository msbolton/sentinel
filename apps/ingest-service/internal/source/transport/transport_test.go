package transport

import (
	"strings"
	"testing"
)

// A remote is a peer address, a topic, a queue or, for a WebSocket feed, the
// URL the feed is configured with, which can carry a user, a token or a key.
// What leaves the process keeps the address and drops the parts that hold
// secrets. Only text with "://" in it is a URL: a topic or queue name is left
// as it is, whatever characters it holds.
func TestRedactRemote(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"userinfo, query and fragment": {"wss://user:tok@host:443/path?key=abc#x", "wss://host:443/path"},
		"user and password":            {"ws://user:tok@feed.example.com/stream", "ws://feed.example.com/stream"},
		"a user that is a token":       {"wss://tok123@feed.example.com/stream", "wss://feed.example.com/stream"},
		"query":                        {"wss://feed.example.com/stream?apikey=abc&v=2", "wss://feed.example.com/stream"},
		"fragment":                     {"wss://feed.example.com/stream#frag", "wss://feed.example.com/stream"},
		"a bare question mark":         {"wss://feed.example.com/stream?", "wss://feed.example.com/stream"},
		"IPv6 host":                    {"wss://u:p@[2001:db8::1]:8443/s?k=v", "wss://[2001:db8::1]:8443/s"},
		"no path":                      {"wss://u:p@feed.example.com?k=v", "wss://feed.example.com"},
		"an @ in the password":         {"wss://user:p@ss@feed.example.com/stream?key=abc", "wss://feed.example.com/stream"},
		"a URL with nothing to strip":  {"wss://feed.example.com:443/a%20b/c", "wss://feed.example.com:443/a%20b/c"},

		// With nothing to strip the text comes back as written, not as a URL
		// would be printed (which lower-cases the scheme and escapes a space).
		"a scheme in capitals": {"WSS://feed.example.com/stream", "WSS://feed.example.com/stream"},
		"a space in the path":  {"wss://feed.example.com/a b", "wss://feed.example.com/a b"},

		// Text with "://" in it that does not parse as a URL is not let through
		// for being malformed: it is cut at the first '?' or '#' and loses the
		// user and password, which sit before the last '@' ahead of the first '/'.
		"a bad escape in the path":         {"wss://u:p@host/%zz?key=abc#f", "wss://host/%zz"},
		"a bad escape in the user":         {"wss://us%zzer:p@host/s?k=v", "wss://host/s"},
		"a bad escape in the fragment":     {"wss://u:p@host/s#frag%zz", "wss://host/s"},
		"a control character in a URL":     {"wss://u:p@host/s\x01?k=v", "wss://host/s\x01"},
		"a space in the host":              {"wss://u:p@ho st/s?k=v", "wss://ho st/s"},
		"a bad port and no path":           {"wss://u:p@host:port?k=v", "wss://host:port"},
		"an @ in the password, unparsable": {"wss://user:p@ss@host/%zz?key=abc", "wss://host/%zz"},
		"an @ in the path is not a user":   {"wss://host/%zz/a@b?k=v", "wss://host/%zz/a@b"},
		"text before the scheme":           {" wss://u:p@host/s?k=v", " wss://host/s"},
		"a digit before the scheme":        {"1wss://u:p@host/s?k=v", "1wss://host/s"},
		"no scheme at all":                 {"://u:p@host?k=v", "://host"},
		"a URL inside a topic":             {"bridge/https://u:p@example.com/x?token=1", "bridge/https://example.com/x"},
		"a URL in a query":                 {"/redirect?to=wss://u:p@host/", "/redirect"},

		// No "://", so not a URL: addresses, topics and queues come back as
		// written, including opaque scheme:rest text that holds a '?', which a
		// URL parser would split.
		"IPv4 and port":                  {"10.0.0.5:4000", "10.0.0.5:4000"},
		"IPv6 and port":                  {"[::1]:9", "[::1]:9"},
		"host and port":                  {"feed.example.com:4000", "feed.example.com:4000"},
		"mqtt topic":                     {"sensors/ais/1", "sensors/ais/1"},
		"mqtt topic with a colon":        {"device:42/status", "device:42/status"},
		"mqtt topic with a colon, query": {"sensors:temp?unit=c", "sensors:temp?unit=c"},
		"mqtt topic that reads as a URL": {"device:42/status?x", "device:42/status?x"},
		"stomp queue":                    {"/queue/ais", "/queue/ais"},
		"stomp queue with a selector":    {"jms.queue:orders?sel=1", "jms.queue:orders?sel=1"},
		"empty":                          {"", ""},

		// Nor do these hold "://", so none is touched, even where it holds a
		// character a URL would treat as a delimiter.
		"garbage":               {"not a url at all", "not a url at all"},
		"a lone scheme marker":  {"::", "::"},
		"a bad escape":          {"%zz", "%zz"},
		"a control character":   {"sensors/\x01ais?x", "sensors/\x01ais?x"},
		"a topic with a hash":   {"sensors/ais#1", "sensors/ais#1"},
		"a topic with a query":  {"sensors/ais?1", "sensors/ais?1"},
		"a topic with an at":    {"user@sensors/ais", "user@sensors/ais"},
		"a path and a question": {"/queue/ais?x=1", "/queue/ais?x=1"},
	} {
		got := RedactRemote(tc.in)
		if got != tc.want {
			t.Errorf("%s: RedactRemote(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
		if again := RedactRemote(got); again != got {
			t.Errorf("%s: redacting twice gives %q, then %q, want it stable", name, got, again)
		}
	}
}

// Redacting must not depend on the text parsing. Whatever follows "://", the
// result has no query, no fragment and no user, however malformed the URL is.
// The check covers every string of up to five characters from an alphabet of
// those that URL syntax cares about, behind prefixes that are and are not a
// scheme.
func TestRedactRemoteLeavesNoSecretPartInAURL(t *testing.T) {
	var suffixes []string
	var grow func(s string)
	grow = func(s string) {
		suffixes = append(suffixes, s)
		if len(s) == 5 {
			return
		}
		for _, c := range "u:@/?#% " {
			grow(s + string(c))
		}
	}
	grow("")
	for _, prefix := range []string{"", "wss", "1", " ", "a/b", "/r?to=", "x#"} {
		for _, suffix := range suffixes {
			in := prefix + "://" + suffix
			got := RedactRemote(in)
			if strings.ContainsAny(got, "?#") {
				t.Fatalf("RedactRemote(%q) = %q, which keeps a query or a fragment", in, got)
			}
			if i := strings.Index(got, "://"); i >= 0 {
				if authority, _, _ := strings.Cut(got[i+len("://"):], "/"); strings.Contains(authority, "@") {
					t.Fatalf("RedactRemote(%q) = %q, which keeps a user", in, got)
				}
			}
		}
	}
}
