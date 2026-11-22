package filekind

import (
	"errors"
	"math"
	"testing"
)

func TestPythonFloatRepr(t *testing.T) {
	cases := map[float64]string{
		0.5: "0.5", 1: "1.0", -1: "-1.0", 0.1: "0.1", 1e16: "1e+16", 1e15: "1000000000000000.0",
		0.0001: "0.0001", 0.00001: "1e-05", 123.456: "123.456", 1.5e300: "1.5e+300", 2.5e-10: "2.5e-10",
		5e-324: "5e-324", math.MaxFloat64: "1.7976931348623157e+308", 1e22: "1e+22", 100: "100.0",
		0: "0.0", math.Copysign(0, -1): "-0.0", 123456789012345680: "1.2345678901234568e+17",
	}
	for f, want := range cases {
		if got := pythonFloatRepr(f); got != want {
			t.Errorf("pythonFloatRepr(%v) = %s, want %s", f, got, want)
		}
	}
}

func TestCanonicalJSON(t *testing.T) {
	good := map[string]string{
		`{ "b": [1, 2.0, -0, -0.0], "a": {} }`: `{"a":{},"b":[1,2.0,0,-0.0]}`,
		"\"\\u2028\\u0007\\/\"":                "\"\u2028\\u0007/\"",
		`{"tf":[7,-3],"decimal":0.5}`:          `{"decimal":0.5,"tf":[7,-3]}`,
		"\"\\ud83d\\ude00\"":                   "\"\U0001F600\"",
	}
	for in, want := range good {
		got, err := CanonicalJSON([]byte(in))
		if err != nil || string(got) != want {
			t.Errorf("CanonicalJSON(%s) = %s, %v, want %s", in, got, err, want)
		}
	}
	for _, in := range []string{`{"a":1,"a":2}`, `NaN`, `"\ud800"`, `"\udc00"`, `1 2`, ``, `[1e400]`, "\"\xff\"", `[1,]`} {
		if _, err := CanonicalJSON([]byte(in)); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("CanonicalJSON(%q) = %v, want ErrInvalidJSON", in, err)
		}
	}
}
