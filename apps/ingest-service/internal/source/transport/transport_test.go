package transport

import "testing"

// A remote is a peer address, a topic, a queue or, for a WebSocket feed, the
// URL the feed is configured with, which can carry a user, a token or a key.
// What leaves the process keeps the address and drops the parts that hold
// secrets; everything that is not a URL is left as it is.
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
		"a URL with nothing to strip":  {"wss://feed.example.com:443/a%20b/c", "wss://feed.example.com:443/a%20b/c"},

		// With nothing to strip the text comes back as written, not as a URL
		// would be printed (which lower-cases the scheme and escapes a space).
		"a scheme in capitals": {"WSS://feed.example.com/stream", "WSS://feed.example.com/stream"},
		"a space in the path":  {"wss://feed.example.com/a b", "wss://feed.example.com/a b"},

		"IPv4 and port": {"10.0.0.5:4000", "10.0.0.5:4000"},
		"IPv6 and port": {"[::1]:9", "[::1]:9"},
		"host and port": {"feed.example.com:4000", "feed.example.com:4000"},
		"mqtt topic":    {"sensors/ais/1", "sensors/ais/1"},
		"stomp queue":   {"/queue/ais", "/queue/ais"},
		"empty":         {"", ""},

		// None of these is a URL with a scheme, so none is touched, even
		// where it holds a character a URL would treat as a delimiter.
		"garbage":               {"not a url at all", "not a url at all"},
		"a lone scheme marker":  {"::", "::"},
		"a bad escape":          {"%zz", "%zz"},
		"a control character":   {"sensors/\x01ais?x", "sensors/\x01ais?x"},
		"a topic with a hash":   {"sensors/ais#1", "sensors/ais#1"},
		"a topic with a query":  {"sensors/ais?1", "sensors/ais?1"},
		"a topic with an at":    {"user@sensors/ais", "user@sensors/ais"},
		"a topic with a colon":  {"device:42/status", "device:42/status"},
		"a path and a question": {"/queue/ais?x=1", "/queue/ais?x=1"},

		// The rule is the URL grammar, not a guess at intent: a topic that is
		// also a URL with a scheme loses a query it carries.
		"a topic that reads as scheme:path?query": {"device:42/status?x", "device:42/status"},
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
