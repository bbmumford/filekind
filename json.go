package filekind

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrInvalidJSON: a document CanonicalJSON cannot hold: not one JSON value,
// not UTF-8, a repeated key, an unpaired surrogate escape, or a number no
// float64 holds.
var ErrInvalidJSON = errors.New("filekind: not a canonical JSON value")

// CanonicalJSON rewrites one JSON document in the one form a TEXT column holds
// JSON in, so equal bodies are equal bytes and digest equal: object keys sorted
// by their UTF-8 bytes, no whitespace, strings in UTF-8 with only the escapes
// Python's json.dumps(ensure_ascii=False) writes, integers as their digits, and
// every other number as Python's repr of the float64 it parses to. The Python
// helper's canonical_json writes the same bytes for the same document; the
// golden vectors hold both to it.
func CanonicalJSON(doc []byte) ([]byte, error) {
	if !utf8.Valid(doc) {
		return nil, fmt.Errorf("%w: not UTF-8", ErrInvalidJSON)
	}
	if err := checkSurrogateEscapes(doc); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var out bytes.Buffer
	if err := canonicalValue(dec, &out); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: more than one value", ErrInvalidJSON)
	}
	return out.Bytes(), nil
}

func canonicalValue(dec *json.Decoder, out *bytes.Buffer) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return canonicalObject(dec, out)
		case '[':
			out.WriteByte('[')
			for first := true; dec.More(); first = false {
				if !first {
					out.WriteByte(',')
				}
				if err := canonicalValue(dec, out); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
			}
			out.WriteByte(']')
			return nil
		}
		return fmt.Errorf("%w: unexpected %v", ErrInvalidJSON, t)
	case string:
		writeJSONString(out, t)
	case json.Number:
		n, err := canonicalNumber(string(t))
		if err != nil {
			return err
		}
		out.WriteString(n)
	case bool:
		if t {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case nil:
		out.WriteString("null")
	default:
		return fmt.Errorf("%w: token %T", ErrInvalidJSON, tok)
	}
	return nil
}

func canonicalObject(dec *json.Decoder, out *bytes.Buffer) error {
	members := map[string][]byte{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("%w: object key %T", ErrInvalidJSON, tok)
		}
		if _, dup := members[key]; dup {
			return fmt.Errorf("%w: repeated key %q", ErrInvalidJSON, key)
		}
		var value bytes.Buffer
		if err := canonicalValue(dec, &value); err != nil {
			return err
		}
		members[key] = value.Bytes()
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			out.WriteByte(',')
		}
		writeJSONString(out, k)
		out.WriteByte(':')
		out.Write(members[k])
	}
	out.WriteByte('}')
	return nil
}

// canonicalNumber keeps an integer's digits (a negative zero becomes 0, as
// Python reads it) and writes any other number as Python's float repr.
func canonicalNumber(n string) (string, error) {
	if !strings.ContainsAny(n, ".eE") {
		// Python's int reads at most 4300 digits by default and refuses more,
		// so an integer past that has no form both helpers write.
		if len(strings.TrimPrefix(n, "-")) > 4300 {
			return "", fmt.Errorf("%w: integer of more than 4300 digits", ErrInvalidJSON)
		}
		if n == "-0" {
			return "0", nil
		}
		return n, nil
	}
	f, err := strconv.ParseFloat(n, 64)
	if err != nil || math.IsInf(f, 0) {
		return "", fmt.Errorf("%w: number %s", ErrInvalidJSON, n)
	}
	return pythonFloatRepr(f), nil
}

// pythonFloatRepr is Python's repr of a finite float64: the shortest digits
// that round-trip, written positionally when the decimal point falls between
// 10^-4 and 10^16 (with ".0" on a whole number) and in exponent form otherwise,
// the exponent signed and at least two digits.
func pythonFloatRepr(f float64) string {
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	sign := ""
	if s[0] == '-' {
		sign, s = "-", s[1:]
	}
	mantissa, exponent, _ := strings.Cut(s, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exp, _ := strconv.Atoi(exponent)
	point := exp + 1
	if -4 < point && point <= 16 {
		switch {
		case point <= 0:
			return sign + "0." + strings.Repeat("0", -point) + digits
		case point >= len(digits):
			return sign + digits + strings.Repeat("0", point-len(digits)) + ".0"
		default:
			return sign + digits[:point] + "." + digits[point:]
		}
	}
	out := digits[:1]
	if len(digits) > 1 {
		out += "." + digits[1:]
	}
	expSign := "+"
	if exp < 0 {
		expSign, exp = "-", -exp
	}
	return sign + out + "e" + expSign + fmt.Sprintf("%02d", exp)
}

// writeJSONString writes s with exactly Python's escapes: the quote, the
// backslash, \b \f \n \r \t, and \u00xx in lower-case hex for every other byte
// below 0x20. Everything else, U+2028 and U+2029 included, is written as is.
func writeJSONString(out *bytes.Buffer, s string) {
	out.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(out, `\u%04x`, c)
			} else {
				out.WriteByte(c)
			}
		}
	}
	out.WriteByte('"')
}

// checkSurrogateEscapes refuses a \u escape of a UTF-16 surrogate that is not
// a high surrogate followed at once by a low one. Go's decoder would read an
// unpaired one as U+FFFD where Python keeps a lone surrogate it cannot encode,
// so the two languages agree only by refusing it.
func checkSurrogateEscapes(doc []byte) error {
	inString := false
	for i := 0; i < len(doc); i++ {
		c := doc[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			if i+1 < len(doc) && doc[i+1] == 'u' {
				v, ok := hex4(doc, i+2)
				if ok && v >= 0xD800 && v <= 0xDBFF {
					w, ok2 := uint16(0), false
					if i+7 < len(doc) && doc[i+6] == '\\' && doc[i+7] == 'u' {
						w, ok2 = hex4(doc, i+8)
					}
					if !ok2 || w < 0xDC00 || w > 0xDFFF {
						return fmt.Errorf("%w: unpaired surrogate escape", ErrInvalidJSON)
					}
					i += 11
					continue
				}
				if ok && v >= 0xDC00 && v <= 0xDFFF {
					return fmt.Errorf("%w: unpaired surrogate escape", ErrInvalidJSON)
				}
			}
			i++
		}
	}
	return nil
}

func hex4(doc []byte, at int) (uint16, bool) {
	if at+4 > len(doc) {
		return 0, false
	}
	v, err := strconv.ParseUint(string(doc[at:at+4]), 16, 16)
	return uint16(v), err == nil
}
