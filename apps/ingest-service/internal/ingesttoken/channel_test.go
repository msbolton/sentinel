package ingesttoken_test

import (
	"testing"

	"github.com/sentinel/ingest-service/internal/ingesttoken"
	"github.com/sentinel/ingest-service/internal/registry"
)

func TestFeedsChannelMatchesRegistry(t *testing.T) {
	if ingesttoken.FeedsChannel != registry.ChannelFeeds {
		t.Fatalf("feedsChannel %q, registry %q", ingesttoken.FeedsChannel, registry.ChannelFeeds)
	}
}
