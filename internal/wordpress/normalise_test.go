package wordpress

import (
	"encoding/json"
	"testing"
)

func TestNormaliseTopLevelScalarsAsWordPressStoresThem(t *testing.T) {
	cases := map[string]string{
		`5`:               `"5"`,
		`-12`:             `"-12"`,
		`1.5`:             `"1.5"`,
		`true`:            `"1"`,
		`false`:           `""`,
		`null`:            `""`,
		`"text"`:          `"text"`,
		`{}`:              `[]`,
		`[]`:              `[]`,
		`{"0":"a","1":5}`: `["a",5]`,
		`{"1":"a"}`:       `{"1":"a"}`,
		`{"b":{"y":true,"x":null},"a":[1.5,false]}`: `{"a":[1.5,false],"b":{"x":null,"y":true}}`,
		`"a/b <c> ä"`: `"a/b <c> ä"`,
	}
	for in, want := range cases {
		value, err := Normalise(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := Canonical(value); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
}

func TestNormaliseRefusesWhatIsNotOneJSONValue(t *testing.T) {
	for _, in := range []string{``, `{`, `1 2`, `nope`, `1]`, `{"a":1}}`, `"a" x`} {
		if _, err := Normalise(in); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

func TestNumbersAreSpelledAsPHPEncodesThem(t *testing.T) {
	cases := map[string]string{
		`1.0`:                      `"1"`,
		`1e2`:                      `"100"`,
		`{"n":1.0,"m":[1e0,2.50]}`: `{"m":[1,2.5],"n":1}`,
	}
	for in, want := range cases {
		value, err := Normalise(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := Canonical(value); got != want {
			t.Errorf("%s: got %s, want %s", in, got, want)
		}
	}
	if !Equal(`{"n":1.0}`, `{"n":1}`) {
		t.Fatal("1.0 and 1 differ")
	}
}

// What PHP 8.4 makes of these floats, from php:8.4-cli: json_encode inside
// an array, and the string cast a top-level option is stored as.
func TestFloatsAreSpelledAsPHP84SpellsThem(t *testing.T) {
	cases := []struct{ in, json, str string }{
		{"0.0000001", "1.0e-7", "1.0E-7"},
		{"1.5e-7", "1.5e-7", "1.5E-7"},
		{"0.0001", "0.0001", "0.0001"},
		{"0.00001", "1.0e-5", "1.0E-5"},
		{"0.5", "0.5", "0.5"},
		{"2.50", "2.5", "2.5"},
		{"100.0", "100", "100"},
		{"1e15", "1000000000000000", "1.0E+15"},
		{"1e16", "10000000000000000", "1.0E+16"},
		{"1e17", "1.0e+17", "1.0E+17"},
		{"1e25", "1.0e+25", "1.0E+25"},
		{"123456.789", "123456.789", "123456.789"},
		{"0.30000000000000004", "0.30000000000000004", "0.3"},
		{"-0.000012345", "-1.2345e-5", "-1.2345E-5"},
		{"1.0", "1", "1"},
		{"0.0", "0", "0"},
		{"12345678901234567890.0", "1.2345678901234567e+19", "1.2345678901235E+19"},
	}
	for _, c := range cases {
		if got := jsonNumber(json.Number(c.in)); got != c.json {
			t.Errorf("json %s: got %s, want %s", c.in, got, c.json)
		}
		if got := number(json.Number(c.in)); got != c.str {
			t.Errorf("string %s: got %s, want %s", c.in, got, c.str)
		}
	}
	// PHP 8.4: json_encode(json_decode('{"a":1.0,"b":1e2,"c":0.0000001,"d":1.50,"e":5}', true))
	value, _ := Normalise(`{"a":1.0,"b":1e2,"c":0.0000001,"d":1.50,"e":5}`)
	if got, want := Canonical(value), `{"a":1,"b":100,"c":1.0e-7,"d":1.5,"e":5}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestEqualIgnoresShapeAndOrder(t *testing.T) {
	if !Equal(`{"b":1,"a":2}`, `{"a":2, "b":1}`) || !Equal(`5`, `"5"`) || !Equal(`{}`, `[]`) {
		t.Fatal("expected equal")
	}
	if Equal(`{"a":1}`, `{"a":"1"}`) {
		t.Fatal("nested types must keep their difference")
	}
}

// The plugin's own canonical JSON of the same value, from its unit tests
// (OptionStoreTest): the two must hash alike.
func TestSHA256MatchesThePlugin(t *testing.T) {
	value, _ := Normalise(`{"url":"https://api.example.com/","secret":"s3cr3t"}`)
	if got, want := SHA256(value), "4d6edaec85858f73acfd15e1d5611a5d0ac31b4b7ea95aa6eae16d5331d05a6f"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
