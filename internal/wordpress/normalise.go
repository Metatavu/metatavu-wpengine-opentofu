package wordpress

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
		return json.Number(jsonNumber(v))
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

// Numbers become what PHP makes of them. A JSON integer stays an integer. A
// float is printed by PHP's php_gcvt: as json_encode writes it inside an
// array (shortest digits, exponent form beyond 17 digits or below 1e-4, no
// ".0" on whole numbers), or as a string cast writes a top-level option
// (14 significant digits, exponent form beyond 14 digits). Checked against
// PHP 8.4's own output in normalise_test.go.

// jsonNumber is a nested number as PHP's json_encode writes it.
func jsonNumber(n json.Number) string {
	if i, ok := integer(n); ok {
		return i
	}
	f, err := n.Float64()
	if err != nil {
		return n.String()
	}
	return phpGcvt(f, 17, true, 'e')
}

// number is a top-level number as PHP's string cast writes it.
func number(n json.Number) string {
	if i, ok := integer(n); ok {
		return i
	}
	f, err := n.Float64()
	if err != nil {
		return n.String()
	}
	return phpGcvt(f, 14, false, 'E')
}

// integer is a JSON integer literal, which PHP decodes as an int.
func integer(n json.Number) (string, bool) {
	if strings.ContainsAny(n.String(), ".eE") {
		return "", false
	}
	i, err := n.Int64()
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(i, 10), true
}

// phpGcvt formats a float as PHP's php_gcvt does: with ndigit significant
// digits (or the shortest that round-trip), in exponent form when the
// decimal point would be more than ndigit digits right, or more than three
// zeros right, of the first digit.
func phpGcvt(f float64, ndigit int, shortest bool, exponent byte) string {
	if f == 0 {
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	precision := ndigit - 1
	if shortest {
		precision = -1
	}
	mantissa, exp, _ := strings.Cut(strconv.FormatFloat(f, 'e', precision, 64), "e")
	digits := strings.TrimRight(strings.Replace(mantissa, ".", "", 1), "0")
	if digits == "" {
		digits = "0"
	}
	power, _ := strconv.Atoi(exp)
	decpt := power + 1
	switch {
	case decpt < -3 || decpt > ndigit:
		out := digits[:1] + "."
		if len(digits) == 1 {
			out += "0"
		} else {
			out += digits[1:]
		}
		expSign := "+"
		if power < 0 {
			expSign, power = "-", -power
		}
		return sign + out + string(exponent) + expSign + strconv.Itoa(power)
	case decpt <= 0:
		return sign + "0." + strings.Repeat("0", -decpt) + digits
	case len(digits) <= decpt:
		return sign + digits + strings.Repeat("0", decpt-len(digits))
	default:
		return sign + digits[:decpt] + "." + digits[decpt:]
	}
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
