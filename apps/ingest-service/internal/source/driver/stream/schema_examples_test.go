package stream

import (
	"encoding/json"
	"testing"

	"github.com/sentinel/ingest-service/internal/source/driver"
)

// The form suggests a value for each secret from the schema's examples: the
// stream driver names the files the TLS material usually lives in, as the tcp
// driver does for the same secrets.
func TestSchemaSecretsHaveExamples(t *testing.T) {
	def, ok := driver.Lookup(ID)
	if !ok {
		t.Fatal("the stream driver is not registered")
	}
	var schema struct {
		Secrets map[string]struct {
			Examples []string `json:"examples"`
		} `json:"x-secrets"`
	}
	if err := json.Unmarshal(def.ConfigSchema, &schema); err != nil {
		t.Fatal(err)
	}
	for secret, want := range map[string]string{
		"tlsCert":  "file:/etc/sentinel/secrets/server.pem",
		"tlsKey":   "file:/etc/sentinel/secrets/server.key",
		"clientCa": "file:/etc/sentinel/secrets/clients-ca.pem",
		"serverCa": "file:/etc/sentinel/secrets/ca.pem",
	} {
		if got := schema.Secrets[secret].Examples; len(got) != 1 || got[0] != want {
			t.Errorf("x-secrets.%s.examples = %q, want [%q]", secret, got, want)
		}
	}
}
