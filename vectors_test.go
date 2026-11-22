package filekind

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeebo/blake3"
)

// The golden vectors both helpers must reproduce (testdata/vectors).
type vectorFile struct {
	Headers []struct {
		File   string `json:"file"`
		Header struct {
			ApplicationID int32 `json:"applicationId"`
			UserVersion   int32 `json:"userVersion"`
		} `json:"header"`
		Checks []struct {
			Kind    string `json:"kind"`
			Newest  int32  `json:"newest"`
			Oldest  int32  `json:"oldest"`
			Outcome string `json:"outcome"`
		} `json:"checks"`
	} `json:"headers"`
	Digests []struct {
		File   string `json:"file"`
		RowSet struct {
			Kind    string   `json:"kind"`
			Set     string   `json:"set"`
			Table   string   `json:"table"`
			Columns []string `json:"columns"`
			Key     []string `json:"key"`
			Where   string   `json:"where"`
			Args    []any    `json:"args"`
		} `json:"rowSet"`
		StreamHex string `json:"streamHex"`
		SHA256    string `json:"sha256"`
		BLAKE3    string `json:"blake3"`
		Rows      uint64 `json:"rows"`
		Outcome   string `json:"outcome"`
	} `json:"digests"`
	Values []struct {
		File    string `json:"file"`
		Kind    string `json:"kind"`
		Outcome string `json:"outcome"`
		Detail  string `json:"detail"`
	} `json:"values"`
	JSON []struct {
		Input     *string `json:"input"`
		InputHex  string  `json:"inputHex"`
		Canonical string  `json:"canonical"`
		Outcome   string  `json:"outcome"`
	} `json:"json"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "vectors", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func kindNamed(t *testing.T, name string) Kind {
	t.Helper()
	e, ok := ByName(name)
	if !ok {
		t.Fatalf("vector names unregistered kind %q", name)
	}
	return e.Kind
}

func TestGoldenHeaders(t *testing.T) {
	ctx := context.Background()
	for _, v := range loadVectors(t).Headers {
		db, path := fixture(t, v.File)
		want := Header{ApplicationID: v.Header.ApplicationID, UserVersion: v.Header.UserVersion}
		if h, err := ReadFileHeader(path); err != nil || h != want {
			t.Errorf("%s: ReadFileHeader = %+v, %v, want %+v", v.File, h, err, want)
		}
		if h, err := Read(ctx, db); err != nil || h != want {
			t.Errorf("%s: Read = %+v, %v, want %+v", v.File, h, err, want)
		}
		for _, c := range v.Checks {
			_, err := Check(ctx, db, Want{Kind: kindNamed(t, c.Kind), Newest: c.Newest, Oldest: c.Oldest})
			if got := outcomeName(err); got != c.Outcome {
				t.Errorf("%s: Check(%s %d..%d) = %s, want %s", v.File, c.Kind, c.Oldest, c.Newest, got, c.Outcome)
			}
		}
	}
}

func TestGoldenDigests(t *testing.T) {
	ctx := context.Background()
	for i, v := range loadVectors(t).Digests {
		db, _ := fixture(t, v.File)
		args := make([]any, len(v.RowSet.Args))
		for j, a := range v.RowSet.Args {
			if n, ok := a.(json.Number); ok {
				x, err := n.Int64()
				if err != nil {
					t.Fatal(err)
				}
				args[j] = x
			} else {
				args[j] = a
			}
		}
		set := RowSet{Kind: kindNamed(t, v.RowSet.Kind), Set: v.RowSet.Set, Table: v.RowSet.Table,
			Columns: v.RowSet.Columns, Key: v.RowSet.Key, Where: v.RowSet.Where, Args: args}
		label := fmt.Sprintf("digest %d (%s %s)", i, v.File, v.RowSet.Set)
		var buf bytes.Buffer
		_, encErr := EncodeRows(ctx, db, set, &buf)
		d, err := DigestRows(ctx, db, set)
		if v.Outcome != "" {
			if got := outcomeName(err); got != v.Outcome || outcomeName(encErr) != v.Outcome {
				t.Errorf("%s: %s / %s, want %s", label, got, outcomeName(encErr), v.Outcome)
			}
			continue
		}
		if err != nil || encErr != nil {
			t.Errorf("%s: %v / %v", label, err, encErr)
			continue
		}
		if v.StreamHex != "" && hex.EncodeToString(buf.Bytes()) != v.StreamHex {
			t.Errorf("%s: stream\n got %x\nwant %s", label, buf.Bytes(), v.StreamHex)
		}
		if d.SHA256Hex() != v.SHA256 || d.BLAKE3Hex() != v.BLAKE3 || d.Rows != v.Rows {
			t.Errorf("%s: %s %s %d, want %s %s %d", label, d.SHA256Hex(), d.BLAKE3Hex(), d.Rows, v.SHA256, v.BLAKE3, v.Rows)
		}
	}
}

func TestGoldenValues(t *testing.T) {
	for _, v := range loadVectors(t).Values {
		db, _ := fixture(t, v.File)
		err := CheckValues(context.Background(), db, kindNamed(t, v.Kind))
		if got := outcomeName(err); got != v.Outcome {
			t.Errorf("%s: CheckValues = %s, want %s", v.File, got, v.Outcome)
		}
		if v.Detail != "" && (err == nil || !strings.HasSuffix(err.Error(), v.Detail)) {
			t.Errorf("%s: CheckValues = %v, want it to name %s", v.File, err, v.Detail)
		}
	}
}

func TestGoldenJSON(t *testing.T) {
	for i, v := range loadVectors(t).JSON {
		in := []byte(nil)
		if v.Input != nil {
			in = []byte(*v.Input)
		} else {
			var err error
			if in, err = hex.DecodeString(v.InputHex); err != nil {
				t.Fatal(err)
			}
		}
		got, err := CanonicalJSON(in)
		if v.Outcome != "" {
			if outcomeName(err) != v.Outcome {
				t.Errorf("json %d (%.40q): %s, want %s", i, in, outcomeName(err), v.Outcome)
			}
			continue
		}
		if err != nil || string(got) != v.Canonical {
			t.Errorf("json %d (%.40q):\n got %.300s %v\nwant %.300s", i, in, got, err, v.Canonical)
		}
	}
}

// blake3Lengths are the input lengths of the BLAKE3 specification's test
// vectors, each input the bytes i mod 251.
var blake3Lengths = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 63, 64, 65, 127, 128, 129, 1023, 1024, 1025, 2048, 2049,
	3072, 3073, 4096, 4097, 5120, 5121, 6144, 6145, 7168, 7169, 8192, 8193, 16384, 31744, 102400}

type blake3Vector struct {
	Length int    `json:"length"`
	Hash   string `json:"hash"`
}

// TestBlake3Vectors holds this module's BLAKE3 to blake3.json, which the
// Python helper's own BLAKE3 must also reproduce, and both to the empty
// input's published hash. FILEKIND_WRITE_BLAKE3=1 writes the file instead.
func TestBlake3Vectors(t *testing.T) {
	var vectors []blake3Vector
	for _, n := range blake3Lengths {
		in := make([]byte, n)
		for i := range in {
			in[i] = byte(i % 251)
		}
		sum := blake3.Sum256(in)
		vectors = append(vectors, blake3Vector{Length: n, Hash: hex.EncodeToString(sum[:])})
	}
	if vectors[0].Hash != "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262" {
		t.Fatalf("BLAKE3 of the empty input = %s", vectors[0].Hash)
	}
	path := filepath.Join("testdata", "vectors", "blake3.json")
	if os.Getenv("FILEKIND_WRITE_BLAKE3") == "1" {
		data, _ := json.MarshalIndent(vectors, "", " ")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored []blake3Vector
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(stored) != fmt.Sprint(vectors) {
		t.Fatal("blake3.json differs from this module's BLAKE3")
	}
}
