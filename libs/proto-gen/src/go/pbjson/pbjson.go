// Package pbjson renders sentinel.v1 messages as JSON in the platform
// convention: protojson's camelCase field names, with enum values written as
// their bare name, i.e. with the "<ENUM_NAME>_" prefix removed
// (ENTITY_SOURCE_ADSB_LOL becomes ADSB_LOL). That is the form the database
// columns store and the generated TypeScript enums use, so the same string
// means the same thing in Postgres, on the wire to the browser, and in Go.
//
// Unmarshal accepts both the bare and the prefixed spelling.
package pbjson

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Marshal encodes m as JSON with bare enum names. Unset fields are omitted,
// as are zero-valued scalars that have no explicit presence.
func Marshal(m proto.Message) ([]byte, error) {
	raw, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(m)
	if err != nil {
		return nil, err
	}
	var obj any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("pbjson: re-reading protojson output: %w", err)
	}
	rewriteEnums(m.ProtoReflect().Descriptor(), obj, stripPrefix)
	return json.Marshal(obj)
}

// MarshalIndent is Marshal with indentation, for logs and fixtures.
func MarshalIndent(m proto.Message, indent string) ([]byte, error) {
	data, err := Marshal(m)
	if err != nil {
		return nil, err
	}
	var obj any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	return json.MarshalIndent(obj, "", indent)
}

// Unmarshal decodes JSON produced by Marshal (or by protojson) into m.
// Unknown fields are ignored so consumers tolerate newer producers.
func Unmarshal(data []byte, m proto.Message) error {
	var obj any
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	rewriteEnums(m.ProtoReflect().Descriptor(), obj, addPrefix)
	fixed, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(fixed, m)
}

// EnumName returns the bare name of an enum value (ADSB_LOL, not
// ENTITY_SOURCE_ADSB_LOL). Unknown numbers render as the number.
func EnumName(e protoreflect.Enum) string {
	ed := e.Descriptor()
	v := ed.Values().ByNumber(e.Number())
	if v == nil {
		return fmt.Sprint(int32(e.Number()))
	}
	return stripPrefix(ed, string(v.Name()))
}

// ParseEnum resolves a bare or prefixed enum name to a value of type E.
// It reports false when the name is unknown.
func ParseEnum[E interface {
	~int32
	Descriptor() protoreflect.EnumDescriptor
}](name string) (E, bool) {
	var zero E
	ed := zero.Descriptor()
	v := ed.Values().ByName(protoreflect.Name(addPrefix(ed, name)))
	if v == nil {
		v = ed.Values().ByName(protoreflect.Name(name))
	}
	if v == nil {
		return zero, false
	}
	return E(v.Number()), true
}

// Prefix is the "<ENUM_NAME>_" prefix protobuf style prepends to the values
// of an enum, e.g. "ENTITY_SOURCE_" for EntitySource.
func Prefix(ed protoreflect.EnumDescriptor) string {
	return upperSnake(string(ed.Name())) + "_"
}

func stripPrefix(ed protoreflect.EnumDescriptor, name string) string {
	return strings.TrimPrefix(name, Prefix(ed))
}

func addPrefix(ed protoreflect.EnumDescriptor, name string) string {
	p := Prefix(ed)
	if strings.HasPrefix(name, p) {
		return name
	}
	return p + name
}

// rewriteEnums walks a decoded JSON value alongside the message descriptor
// and applies fn to every enum-valued string it finds. Well-known types such
// as Timestamp are emitted by protojson as strings and are skipped because
// their JSON shape is not an object.
func rewriteEnums(md protoreflect.MessageDescriptor, v any, fn func(protoreflect.EnumDescriptor, string) string) {
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		val, present := obj[fd.JSONName()]
		if !present {
			val, present = obj[string(fd.Name())]
			if !present {
				continue
			}
		}
		key := fd.JSONName()
		if _, ok := obj[key]; !ok {
			key = string(fd.Name())
		}
		switch {
		case fd.IsMap():
			m, ok := val.(map[string]any)
			if !ok {
				continue
			}
			vd := fd.MapValue()
			for k, mv := range m {
				switch vd.Kind() {
				case protoreflect.EnumKind:
					if s, ok := mv.(string); ok {
						m[k] = fn(vd.Enum(), s)
					}
				case protoreflect.MessageKind:
					rewriteEnums(vd.Message(), mv, fn)
				}
			}
		case fd.IsList():
			list, ok := val.([]any)
			if !ok {
				continue
			}
			for j, item := range list {
				switch fd.Kind() {
				case protoreflect.EnumKind:
					if s, ok := item.(string); ok {
						list[j] = fn(fd.Enum(), s)
					}
				case protoreflect.MessageKind:
					rewriteEnums(fd.Message(), item, fn)
				}
			}
		case fd.Kind() == protoreflect.EnumKind:
			if s, ok := val.(string); ok {
				obj[key] = fn(fd.Enum(), s)
			}
		case fd.Kind() == protoreflect.MessageKind:
			rewriteEnums(fd.Message(), val, fn)
		}
	}
}

// upperSnake converts a CamelCase identifier to UPPER_SNAKE the way protoc
// does for enum prefixes: EntitySource -> ENTITY_SOURCE, AISData -> AIS_DATA.
func upperSnake(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}
