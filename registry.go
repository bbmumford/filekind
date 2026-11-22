package filekind

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Prefix is the three bytes 'O', 'R', 'B' every registered application_id
// starts with; the low byte is the kind.
const Prefix uint32 = 0x4F524200

// Kind is the low byte of a file's application_id. The zero value is never
// registered, so an unset Kind is refused wherever one is expected.
type Kind uint8

// Kinds registered in kinds.tsv. TestConstantsMatchTheRegistry holds every
// constant to its row and every row to a constant, so the two cannot drift.
const (
	OrbtrdSystemStore    Kind = 0x01
	AccountStore         Kind = 0x02
	Lad                  Kind = 0x03
	TursoraftLocal       Kind = 0x04
	ScopeReplica         Kind = 0x05
	DevicePolicyStore    Kind = 0x06
	AuditSegment         Kind = 0x10
	AuditExport          Kind = 0x11
	RunLedger            Kind = 0x20
	EncodedIds           Kind = 0x21
	RunEvents            Kind = 0x22
	RunRecords           Kind = 0x23
	Lineage              Kind = 0x24
	MemoryBank           Kind = 0x25
	ContinuityStore      Kind = 0x26
	ConversionArtifact   Kind = 0x27
	EfferentGateRecord   Kind = 0x28
	TeacherFieldRecord   Kind = 0x29
	RemovalLedger        Kind = 0x40
	DecisionIndex        Kind = 0x41
	GuardIndex           Kind = 0x42
	PluginContributions  Kind = 0x43
	DocsSearchIndex      Kind = 0x44
	ImplementationLedger Kind = 0x45
	BillingWitness       Kind = 0x50
	UploaderLedger       Kind = 0x51
	Fixture              Kind = 0xff
)

// ApplicationID is the value PRAGMA application_id holds for a file of kind k.
// Every registered value is below 2^31, so it reads back as the same positive
// number from an engine that returns the header field as a signed integer.
func (k Kind) ApplicationID() int32 { return int32(Prefix | uint32(k)) }

// String names k by its registry row, or by its byte when no row holds it.
func (k Kind) String() string {
	if e, ok := Lookup(k); ok {
		return e.Name
	}
	return fmt.Sprintf("kind 0x%02x", uint8(k))
}

// KindOf splits an application_id into its kind byte. ok is false for a value
// outside the 'ORB' prefix, which is a file another application stamped or one
// nothing stamped.
func KindOf(applicationID int32) (Kind, bool) {
	if uint32(applicationID)&0xFFFFFF00 != Prefix {
		return 0, false
	}
	return Kind(uint32(applicationID) & 0xFF), true
}

// Values names how a kind stores its values. A Base3 kind holds every value as
// base-3 text or a base-3 blob, never as an INTEGER or REAL value outside a
// rowid alias; CheckValues enforces it. A Standard kind uses SQL integers as
// usual. A Radix kind's rows each name the radix their numbers are written in,
// so only the kind's own schema can say which values must be base 3, and
// CheckValues leaves it to that schema's tests.
type Values uint8

const (
	Standard Values = iota + 1
	Base3
	Radix
)

func (v Values) String() string {
	switch v {
	case Standard:
		return "sql"
	case Base3:
		return "base3"
	case Radix:
		return "radix"
	}
	return "values " + strconv.Itoa(int(v))
}

// Entry is one row of kinds.tsv.
//
// Generation is the format generation the registry publishes as current: a
// writer stamps new files with it, and a reader whose code knows no later
// generation passes it to Check as the newest it reads.
type Entry struct {
	Kind       Kind
	Name       string
	Lane       string
	Generation int32
	Encrypted  bool
	Values     Values
	Use        string
}

//go:embed kinds.tsv
var registryTSV string

var (
	registryOnce sync.Once
	registry     []Entry
	registryErr  error
)

// parseRegistry reads kinds.tsv: a header row, then one row per kind with
// exactly the header's seven tab-separated columns. Kind bytes and names are
// unique, bytes ascend, and 0x00 is never a kind.
func parseRegistry(text string) ([]Entry, error) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	const header = "kind\tname\tlane\tgeneration\tencrypted\tvalues\tuse"
	if len(lines) == 0 || lines[0] != header {
		return nil, fmt.Errorf("filekind: kinds.tsv header is not %q", header)
	}
	var entries []Entry
	names := map[string]bool{}
	last := -1
	for i, line := range lines[1:] {
		row := i + 2
		cols := strings.Split(line, "\t")
		if len(cols) != 7 {
			return nil, fmt.Errorf("filekind: kinds.tsv row %d has %d columns, want 7", row, len(cols))
		}
		if len(cols[0]) != 2 || strings.ToLower(cols[0]) != cols[0] {
			return nil, fmt.Errorf("filekind: kinds.tsv row %d kind %q is not two lower-case hex digits", row, cols[0])
		}
		b, err := strconv.ParseUint(cols[0], 16, 8)
		if err != nil || b == 0 {
			return nil, fmt.Errorf("filekind: kinds.tsv row %d kind %q is not a byte from 01 to ff", row, cols[0])
		}
		if int(b) <= last {
			return nil, fmt.Errorf("filekind: kinds.tsv row %d kind %s does not ascend", row, cols[0])
		}
		last = int(b)
		if cols[1] == "" || names[cols[1]] {
			return nil, fmt.Errorf("filekind: kinds.tsv row %d name %q is empty or repeated", row, cols[1])
		}
		names[cols[1]] = true
		gen, err := strconv.ParseInt(cols[3], 10, 32)
		if err != nil || gen < 1 {
			return nil, fmt.Errorf("filekind: kinds.tsv row %d generation %q is not a whole number from 1", row, cols[3])
		}
		var encrypted bool
		switch cols[4] {
		case "yes":
			encrypted = true
		case "no":
		default:
			return nil, fmt.Errorf("filekind: kinds.tsv row %d encrypted %q is not yes or no", row, cols[4])
		}
		var values Values
		switch cols[5] {
		case "sql":
			values = Standard
		case "base3":
			values = Base3
		case "radix":
			values = Radix
		default:
			return nil, fmt.Errorf("filekind: kinds.tsv row %d values %q is not sql, base3 or radix", row, cols[5])
		}
		if cols[2] == "" || cols[6] == "" {
			return nil, fmt.Errorf("filekind: kinds.tsv row %d has an empty lane or use", row)
		}
		entries = append(entries, Entry{
			Kind: Kind(b), Name: cols[1], Lane: cols[2], Generation: int32(gen),
			Encrypted: encrypted, Values: values, Use: cols[6],
		})
	}
	return entries, nil
}

func loadRegistry() ([]Entry, error) {
	registryOnce.Do(func() { registry, registryErr = parseRegistry(registryTSV) })
	return registry, registryErr
}

// Registry returns every registered kind in ascending byte order. It panics if
// the embedded kinds.tsv does not parse, which TestRegistryParses rules out
// for every build of this package.
func Registry() []Entry {
	entries, err := loadRegistry()
	if err != nil {
		panic(err)
	}
	return append([]Entry(nil), entries...)
}

// Lookup returns the registry row for k.
func Lookup(k Kind) (Entry, bool) {
	entries, err := loadRegistry()
	if err != nil {
		return Entry{}, false
	}
	for _, e := range entries {
		if e.Kind == k {
			return e, true
		}
	}
	return Entry{}, false
}

// ByName returns the registry row named name.
func ByName(name string) (Entry, bool) {
	entries, err := loadRegistry()
	if err != nil {
		return Entry{}, false
	}
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}
