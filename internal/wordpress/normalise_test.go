package wordpress

import "testing"

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
