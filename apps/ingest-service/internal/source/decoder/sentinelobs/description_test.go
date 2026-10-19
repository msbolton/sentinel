package sentinelobs

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sentinel/ingest-service/internal/source/decoder"
)

// The decoder's description is what the catalog shows, and it is the only place
// that says a JSON line may hold a batch, and how big.
func TestDescriptionMentionsTheJSONBatch(t *testing.T) {
	def, ok := decoder.Lookup(Name)
	if !ok {
		t.Fatal("the decoder is not registered")
	}
	for _, want := range []string{"JSON array", fmt.Sprintf("up to %d observations", maxBatch)} {
		if !strings.Contains(def.Description, want) {
			t.Errorf("Description = %q, want it to mention %q", def.Description, want)
		}
	}
}
