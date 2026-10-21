package mgmt

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// errorKey is a key with "err" in its name that something writes into the
// status detail of a feed: a map literal, an assignment into a map, or a field
// a struct marshals under that name.
type errorKey struct {
	file string // the file that writes it, as a path under internal/
	key  string
	// why says what the key holds, and so what the status gate does with it.
	why string
	// doc is a detail as the writer writes the key, with some text in it, and
	// kept is what the gate leaves of doc: no text for a key that is hidden, all
	// of it for one that is not. Empty for a key that is not written to the
	// detail by itself.
	doc, kept string
}

// errorKeys is every such key, and how the gate treats it. The status gate
// hides the texts a decoder's error can repeat from what a source sent, by the
// keys they are written under, so a key it does not know about is a text it
// does not hide. TestEveryErrorKeyIsAccountedFor fails when code writes a key
// that is not listed here, or when a listed key is no longer written, so that
// adding one means deciding.
var errorKeys = []errorKey{
	// What the gate hides.
	{"source/driver/stream/stream.go", "lastDecodeError", "the stream driver's copy of the last decode error: removed",
		`{"lastDecodeError":"text"}`, `{}`},
	{"feed/supervisor.go", "lastDecodeError", "the supervisor's copy, taken from the runner for every driver: removed",
		`{"lastDecodeError":"text"}`, `{}`},
	{"source/transport/guard.go", "lastError", "each sender's last decode error, in senders: removed",
		`{"senders":[{"remote":"a:1","lastError":"text"}]}`, `{"senders":[{"remote":"a:1"}]}`},
	{"source/driver/httpjson/httpjson.go", "lastError", "httpjson's last poll error, which after a parse failure is the decode text: blanked, the key kept",
		`{"lastError":"text"}`, `{"lastError":""}`},
	{"source/driver/celestrak/celestrak.go", "errors", "celestrak's error for each selection, a parse failure's text among them: blanked, the keys kept",
		`{"errors":{"group:active":"text"}}`, `{"errors":{"group:active":""}}`},

	// What it leaves, and why.
	{"source/driver/aisstream/conn.go", "lastError", "a connection's own dial, subscription or server-reported error, nested under connections: Sentinel decoded no payload to make it",
		`{"connections":{"area":{"lastError":"text"}}}`, `{"connections":{"area":{"lastError":"text"}}}`},
	{"source/driver/aisstream/conn.go", "error", "the field aisstream.io's own error message is read from; its text goes to the connection's lastError above, and this key is not written to the detail",
		"", ""},
	{"source/driver/aisstream/aisstream.go", "densityError", "the error from publishing density grids to Kafka: a producer's, not a payload's",
		`{"densityError":"text"}`, `{"densityError":"text"}`},
	{"source/driver/aisstream/aisstream.go", "errors", "a count of messages that did not decode: a number, which the gate keeps",
		`{"errors":7}`, `{"errors":7}`},
	{"source/driver/intelsky/intelsky.go", "errors", "a count of failed polls: a number, which the gate keeps",
		`{"errors":7}`, `{"errors":7}`},
	{"feed/supervisor.go", "deliveryErrors", "an outbound counter",
		`{"deliveryErrors":3}`, `{"deliveryErrors":3}`},
	{"source/transport/guard.go", "senderRateLimited", "a listener's count of messages dropped by per-sender limits (its name only contains \"err\" by accident): a number, which the gate keeps",
		`{"senderRateLimited":4}`, `{"senderRateLimited":4}`},
	{"source/driver/grpcin/health.go", "terrainClampSkipped", "a grpc connection's count of radar targets the terrain could not answer for (its name only contains \"err\" by accident): a number, which the gate keeps",
		`{"terrainClampSkipped":4}`, `{"terrainClampSkipped":4}`},
}

// errorKeysWritten finds the keys with "err" in their name that the Go files
// under roots write, as "file key" with the file as a path under internal/. It
// reads the code rather than running it, because a driver adds such a key only
// when it fails. It takes a string key of a map literal, a string index on the
// left of an assignment and the name in a json struct tag, which is how every
// detail key reaches a status today. A key built at run time, or one whose name
// has no "err" in it, is not found.
func errorKeysWritten(t *testing.T, roots ...string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	fset := token.NewFileSet()
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(filepath.ToSlash(path), "../")
			note := func(key string) {
				if strings.Contains(strings.ToLower(key), "err") {
					found[rel+" "+key] = true
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.KeyValueExpr: // "key": value in a map literal
					if key, ok := stringLiteral(x.Key); ok {
						note(key)
					}
				case *ast.AssignStmt: // m["key"] = value
					for _, lhs := range x.Lhs {
						if index, ok := lhs.(*ast.IndexExpr); ok {
							if key, ok := stringLiteral(index.Index); ok {
								note(key)
							}
						}
					}
				case *ast.Field: // Name T `json:"key"`
					if x.Tag != nil {
						if key := jsonName(x.Tag.Value); key != "" {
							note(key)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("reading %s: %v", root, err)
		}
	}
	return found
}

func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// jsonName is the name in the json tag of a struct field, "" if it has none.
func jsonName(tag string) string {
	tag, err := strconv.Unquote(tag)
	if err != nil {
		return ""
	}
	value, ok := reflect.StructTag(tag).Lookup("json")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(value, ",")
	if name == "-" {
		return ""
	}
	return name
}

// The status gate finds the texts it hides by the keys they are written under.
// Code that later writes another error-like key into a status, a new driver or
// a new failure in an old one, would leave its text showing to callers not
// cleared for the feed unless somebody decides about the key. This reads the
// code that builds a detail and fails on any such key that is not listed, and on
// any listed key that is gone, and checks that the gate does with each what the
// list says it does.
func TestEveryErrorKeyIsAccountedFor(t *testing.T) {
	// The code that writes a status detail: the drivers, the transports whose
	// state they merge in, and the supervisor that adds its own keys.
	written := errorKeysWritten(t, "../source/driver", "../source/transport", "../feed")
	if len(written) == 0 {
		t.Fatal("no error key found in the code: the search is broken")
	}

	listed := map[string]errorKey{}
	for _, k := range errorKeys {
		id := k.file + " " + k.key
		if _, again := listed[id]; again {
			t.Errorf("%s is listed twice", id)
		}
		listed[id] = k
	}

	var unknown, gone []string
	for id := range written {
		if _, ok := listed[id]; !ok {
			unknown = append(unknown, id)
		}
	}
	for id := range listed {
		if !written[id] {
			gone = append(gone, id)
		}
	}
	sort.Strings(unknown)
	sort.Strings(gone)
	for _, id := range unknown {
		file, key, _ := strings.Cut(id, " ")
		t.Errorf("%s writes %q, which the status gate does not know. If its text can repeat what a source sent, "+
			"hide it for callers not cleared for the feed (withoutErrorTexts and blankedDetailKeys in clearance.go); "+
			"then add the key to errorKeys in this file, with a detail as the writer writes it and what the gate leaves of it. "+
			"If it holds nothing a payload can reach, add it as left alone, and say why.", file, key)
	}
	for _, id := range gone {
		t.Errorf("%s is in errorKeys but nothing writes it any more: remove it", id)
	}

	for _, k := range errorKeys {
		if k.doc == "" {
			continue
		}
		got := withoutErrorTexts(json.RawMessage(k.doc))
		if !reflect.DeepEqual(decodeAny(t, got), decodeAny(t, json.RawMessage(k.kept))) {
			t.Errorf("%s %s (%s): the gate turns %s into %s, want %s", k.file, k.key, k.why, k.doc, got, k.kept)
		}
	}
}
