package wordpress

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Normalise turns a declared option value (JSON) into the shape WordPress
// stores (provider D16): at the top level a number becomes its decimal
// string, true becomes "1", and false and null become ""; an object is a PHP
// array, so {} equals [] and an object keyed 0..n-1 is a list. Inside
// arrays, values keep their JSON types.
func Normalise(raw string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("not JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("not one JSON value")
	}
	return normalise(value, true), nil
}

func normalise(value any, top bool) any {
	switch v := value.(type) {
	case nil:
		if top {
			return ""
		}
		return nil
	case bool:
		if !top {
			return v
		}
		if v {
			return "1"
		}
		return ""
	case json.Number:
		if top {
			return number(v)
		}
		return json.Number(number(v))
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalise(item, false)
		}
		return out
	case map[string]any:
		if len(v) == 0 {
			return []any{}
		}
		// PHP turns keys 0..n-1, in order, into a list.
		list := make([]any, len(v))
		for i := range list {
			item, ok := v[strconv.Itoa(i)]
			if !ok {
				list = nil
				break
			}
			list[i] = normalise(item, false)
		}
		if list != nil {
			return list
		}
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = normalise(item, false)
		}
		return out
	default:
		return v
	}
}

// number is a JSON number as PHP prints it: an integer as an integer, and a
// float without a zero fraction (json_encode without
// JSON_PRESERVE_ZERO_FRACTION), so 1, 1.0 and 1e0 are all 1. Floats in
// exponent form beyond 1e15 may still differ from PHP's spelling.
func number(n json.Number) string {
	if i, err := n.Int64(); err == nil {
		return strconv.FormatInt(i, 10)
	}
	f, err := n.Float64()
	if err != nil {
		return n.String()
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// Canonical is a normalised value's canonical JSON (plugin D17): object keys
// sorted at every level, no escaped slashes, Unicode or HTML characters.
func Canonical(value any) string {
	var out bytes.Buffer
	canonical(&out, value)
	return out.String()
}

func canonical(out *bytes.Buffer, value any) {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			canonical(out, key)
			out.WriteByte(':')
			canonical(out, v[key])
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			canonical(out, item)
		}
		out.WriteByte(']')
	default:
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(v)
		out.Write(bytes.TrimRight(encoded.Bytes(), "\n"))
	}
}

// SHA256 is the hash the plugin returns for a sensitive option (plugin D9,
// D17): the SHA-256 of the normalised value's canonical JSON.
func SHA256(value any) string {
	sum := sha256.Sum256([]byte(Canonical(value)))
	return hex.EncodeToString(sum[:])
}

// Equal tells whether two values are the same once normalised.
func Equal(a, b string) bool {
	na, errA := Normalise(a)
	nb, errB := Normalise(b)
	return errA == nil && errB == nil && Canonical(na) == Canonical(nb)
}
