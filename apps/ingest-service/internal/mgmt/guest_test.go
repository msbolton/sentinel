package mgmt

import (
	"encoding/json"
	"net/http"
	"testing"
)

// guestFeedServer is a server whose opensky feed has a configuration and a
// secret reference, so that hiding them is observable.
func guestFeedServer(t *testing.T) func(path string, roles string) []byte {
	t.Helper()
	srv, store, _ := newServer(t, "")
	f := store.feeds["opensky"]
	f.Config = json.RawMessage(openskySecret)
	f.SecretRefs = map[string]string{"clientSecret": "OPENSKY_CLIENT_SECRET"}
	store.feeds["opensky"] = f
	return func(path, roles string) []byte {
		resp, out := do(t, srv, "GET", path, "", reqOpts{roles: roles})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s as %q: %d %s", path, roles, resp.StatusCode, out)
		}
		return out
	}
}

// checkGuestFeed is the checks on one feed as a client reads it.
func checkGuestFeed(t *testing.T, feed map[string]any, hidden bool) {
	t.Helper()
	for _, k := range []string{"id", "name", "driver", "enabled", "status"} {
		if _, ok := feed[k]; !ok {
			t.Errorf("%v: %s is missing", feed["id"], k)
		}
	}
	config, _ := feed["config"].(map[string]any)
	refs, _ := feed["secretRefs"].(map[string]any)
	if hidden {
		if len(config) != 0 || config == nil {
			t.Errorf("%v: config = %v, want {}", feed["id"], feed["config"])
		}
		if len(refs) != 0 {
			t.Errorf("%v: secretRefs = %v, want none", feed["id"], feed["secretRefs"])
		}
		return
	}
	if feed["id"] == "opensky" && (len(config) == 0 || len(refs) == 0) {
		t.Errorf("opensky: config = %v, secretRefs = %v, want both kept", feed["config"], feed["secretRefs"])
	}
}

func TestGuestFeedListHasNoConfig(t *testing.T) {
	get := guestFeedServer(t)
	var feeds []map[string]any
	if err := json.Unmarshal(get("/feeds", "sentinel-viewer,sentinel-guest"), &feeds); err != nil {
		t.Fatal(err)
	}
	if len(feeds) == 0 {
		t.Fatal("no feeds")
	}
	for _, f := range feeds {
		checkGuestFeed(t, f, true)
	}
}

func TestViewerFeedListKeepsConfig(t *testing.T) {
	get := guestFeedServer(t)
	var feeds []map[string]any
	if err := json.Unmarshal(get("/feeds", "sentinel-viewer"), &feeds); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, f := range feeds {
		checkGuestFeed(t, f, false)
		seen = seen || f["id"] == "opensky"
	}
	if !seen {
		t.Fatal("opensky is not in the list")
	}
}

func TestGuestFeedGetHasNoConfig(t *testing.T) {
	get := guestFeedServer(t)
	checkGuestFeed(t, decodeObject(t, get("/feeds/opensky", "sentinel-viewer,sentinel-guest")), true)
	checkGuestFeed(t, decodeObject(t, get("/feeds/opensky", "sentinel-viewer")), false)
}
