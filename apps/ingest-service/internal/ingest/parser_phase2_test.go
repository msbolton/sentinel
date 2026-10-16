package ingest

import (
	"strings"
	"testing"
)

func TestParseGenericBlankPayloadDoesNotPanic(t *testing.T) {
	p := NewParser()
	for _, payload := range []string{" ", "\n", "\t \r\n"} {
		if _, err := p.ParseGeneric("tcp", []byte(payload)); err == nil {
			t.Errorf("%q: expected an error", payload)
		}
	}
}

func TestParseGenericStampsDecoder(t *testing.T) {
	p := NewParser()
	cases := map[string]string{
		`{"entity_id":"a","latitude":1,"longitude":2}`: DecoderJSON,
		"MSG,3,1,1,ABC123,1,2024/01/01,00:00:00.000,2024/01/01,00:00:00.000,,35000,,,41.9,-87.9,,,,,,0": DecoderSBS,
	}
	for payload, want := range cases {
		e, err := p.ParseGeneric("tcp", []byte(payload))
		if err != nil {
			t.Fatalf("%s: %v", payload, err)
		}
		if e.Decoder != want || e.Source != "tcp" {
			t.Errorf("%s: decoder=%q source=%q", payload, e.Decoder, e.Source)
		}
	}
}

func TestParseADSBUpperCasesICAO(t *testing.T) {
	p := NewParser()
	e, err := p.ParseADSB([]byte("MSG,3,1,1,abc123,1,2024/01/01,00:00:00.000,2024/01/01,00:00:00.000,,35000,,,41.9,-87.9,,,,,,0"))
	if err != nil {
		t.Fatal(err)
	}
	if e.EntityID != "ICAO-ABC123" || !strings.EqualFold(e.ADSBData.ICAOHex, "ABC123") || e.ADSBData.ICAOHex != "ABC123" {
		t.Errorf("entity id %q icao %q", e.EntityID, e.ADSBData.ICAOHex)
	}
}
