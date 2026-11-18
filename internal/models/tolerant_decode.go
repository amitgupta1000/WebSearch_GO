package models

// tolerant_decode.go is the reflective engine behind the package's tolerant
// decoding: every structured-output struct gets a one-line UnmarshalJSON that
// calls decodeTolerantStruct (see tolerant_targets.go), and this walker coerces
// each field from whatever shape the model actually emitted to the shape the
// field declares. Strings accept numbers and booleans, ints accept numeric
// strings and floats, floats accept numeric strings, bools accept "true"/"yes",
// string slices accept a lone string, struct slices skip junk elements, and
// nested structs are walked the same way. A value the field cannot take is
// skipped -- never fatal.

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// decodeTolerantStruct unmarshals a JSON object into a struct by walking its
// fields through setTolerantField. A non-object payload is refused so callers
// can fall back to the default decoder.
func decodeTolerantStruct(data []byte, target any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return &json.UnmarshalTypeError{Value: "object", Type: value.Type()}
	}
	setStructFields(value.Elem(), fields)
	return nil
}

// setStructFields walks one struct's exported fields against a decoded object.
func setStructFields(target reflect.Value, fields map[string]json.RawMessage) {
	kind := target.Type()
	for index := 0; index < kind.NumField(); index++ {
		field := kind.Field(index)
		if field.PkgPath != "" {
			continue // unexported
		}
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = field.Name
		}
		raw, exists := fields[tag]
		if !exists {
			continue
		}
		setTolerantField(target.Field(index), raw)
	}
}

// setTolerantField writes one raw value into a field, coercing scalars and
// walking nested structs; it reports whether anything was written.
//
// A type with its own UnmarshalJSON keeps it: CompanyProfile (numeric metrics,
// string-or-object locations), Location (object or bare string) and the
// demand-chain reviews all carry bespoke handling the generic walker cannot
// reproduce. Delegation happens first; a bespoke decoder that refuses the
// shape falls through to the generic coercion, which fills what it can.
func setTolerantField(field reflect.Value, raw json.RawMessage) bool {
	if !field.CanSet() || rawIsAbsent(raw) {
		return false
	}
	if field.CanAddr() {
		if unmarshaler, ok := field.Addr().Interface().(json.Unmarshaler); ok {
			if err := unmarshaler.UnmarshalJSON(raw); err == nil {
				return true
			}
		}
	}
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		return setTolerantField(field.Elem(), raw)
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString(flexibleText(raw))
		return true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if value, ok := flexibleIntNumber(raw); ok {
			field.SetInt(int64(value))
			return true
		}
		return false
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if value, ok := flexibleIntNumber(raw); ok && value >= 0 {
			field.SetUint(uint64(value))
			return true
		}
		return false
	case reflect.Float32, reflect.Float64:
		if value, ok := flexibleFloatNumber(raw); ok {
			field.SetFloat(value)
			return true
		}
		return false
	case reflect.Bool:
		if value, ok := flexibleBoolValue(raw); ok {
			field.SetBool(value)
			return true
		}
		return false
	case reflect.Slice:
		return setTolerantSlice(field, raw)
	case reflect.Map:
		return setTolerantMap(field, raw)
	case reflect.Struct:
		if field.Type() == reflect.TypeOf(time.Time{}) {
			// Time values carry their own text formats; a value they refuse
			// stays zero instead of failing the containing struct.
			_ = json.Unmarshal(raw, field.Addr().Interface())
			return true
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return false
		}
		setStructFields(field, fields)
		return true
	default:
		return false
	}
}

// setTolerantSlice handles every slice kind the models declare: text slices
// accept arrays of scalars or a lone scalar, struct slices decode each element
// and skip junk, and other slices fall back to the default decoder per element.
func setTolerantSlice(field reflect.Value, raw json.RawMessage) bool {
	element := field.Type().Elem()
	if element.Kind() == reflect.String {
		values := flexibleTextList(raw)
		if len(values) == 0 {
			return false
		}
		out := reflect.MakeSlice(field.Type(), 0, len(values))
		for _, value := range values {
			out = reflect.Append(out, reflect.ValueOf(value))
		}
		field.Set(out)
		return true
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		entries = []json.RawMessage{raw}
	}
	out := reflect.MakeSlice(field.Type(), 0, len(entries))
	wrote := false
	for _, entry := range entries {
		if rawIsAbsent(entry) {
			continue
		}
		item := reflect.New(element).Elem()
		if setTolerantField(item, entry) {
			out = reflect.Append(out, item)
			wrote = true
		}
	}
	if wrote {
		field.Set(out)
	}
	return wrote
}

// setTolerantMap handles string-keyed maps (OnlinePresence-style), coercing
// each value to the declared element kind.
func setTolerantMap(field reflect.Value, raw json.RawMessage) bool {
	if field.Type().Key().Kind() != reflect.String {
		return false
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return false
	}
	element := field.Type().Elem()
	out := reflect.MakeMap(field.Type())
	for key, entry := range entries {
		if rawIsAbsent(entry) {
			continue
		}
		switch element.Kind() {
		case reflect.String:
			out.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(flexibleText(entry)))
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if value, ok := flexibleIntNumber(entry); ok {
				number := reflect.New(element).Elem()
				number.SetInt(int64(value))
				out.SetMapIndex(reflect.ValueOf(key), number)
			}
		case reflect.Float32, reflect.Float64:
			if value, ok := flexibleFloatNumber(entry); ok {
				number := reflect.New(element).Elem()
				number.SetFloat(value)
				out.SetMapIndex(reflect.ValueOf(key), number)
			}
		default:
			item := reflect.New(element).Elem()
			if setTolerantField(item, entry) {
				out.SetMapIndex(reflect.ValueOf(key), item)
			}
		}
	}
	if out.Len() == 0 {
		return false
	}
	field.Set(out)
	return true
}

// DecodeTolerant is the exported entry point for packages outside models that hold
// their own LLM target types (legal extraction, related-entity extraction, the
// document triage decisions). One line per type, the same drift contract.
func DecodeTolerant(data []byte, target any) error {
	return decodeTolerantStruct(data, target)
}
