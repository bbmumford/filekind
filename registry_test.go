package filekind

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRegistryParses(t *testing.T) {
	entries, err := parseRegistry(registryTSV)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("kinds.tsv holds no kinds")
	}
	if e, ok := Lookup(RunLedger); !ok || e.Name != "runLedger" || e.Generation != 4 || e.Values != Radix {
		t.Fatalf("Lookup(RunLedger) = %+v, %v", e, ok)
	}
	if e, ok := ByName("auditSegment"); !ok || e.Kind != AuditSegment || e.Encrypted {
		t.Fatalf("ByName(auditSegment) = %+v, %v", e, ok)
	}
	if _, ok := Lookup(0); ok {
		t.Fatal("kind 0 is registered")
	}
}

// TestConstantsMatchTheRegistry holds every exported constant to the row of
// the same name and byte, and every row to a constant.
func TestConstantsMatchTheRegistry(t *testing.T) {
	constants := map[string]Kind{
		"orbtrdSystemStore": OrbtrdSystemStore, "accountStore": AccountStore, "lad": Lad,
		"tursoraftLocal": TursoraftLocal, "scopeReplica": ScopeReplica, "devicePolicyStore": DevicePolicyStore,
		"auditSegment": AuditSegment, "auditExport": AuditExport, "runLedger": RunLedger,
		"encodedIds": EncodedIds, "runEvents": RunEvents, "runRecords": RunRecords, "lineage": Lineage,
		"memoryBank": MemoryBank, "continuityStore": ContinuityStore, "conversionArtifact": ConversionArtifact,
		"efferentGateRecord": EfferentGateRecord, "teacherFieldRecord": TeacherFieldRecord,
		"removalLedger": RemovalLedger, "decisionIndex": DecisionIndex, "guardIndex": GuardIndex,
		"pluginContributions": PluginContributions, "docsSearchIndex": DocsSearchIndex,
		"implementationLedger": ImplementationLedger, "billingWitness": BillingWitness,
		"uploaderLedger": UploaderLedger, "fixture": Fixture,
	}
	rows := Registry()
	if len(rows) != len(constants) {
		t.Fatalf("%d rows, %d constants", len(rows), len(constants))
	}
	for _, e := range rows {
		k, ok := constants[e.Name]
		if !ok || k != e.Kind {
			t.Fatalf("row %s (0x%02x) has constant %v, %v", e.Name, uint8(e.Kind), k, ok)
		}
	}
}

// TestPythonCopyMatchesTheRegistry: the Python package ships a copy of
// kinds.tsv, and it must be byte for byte the registry.
func TestPythonCopyMatchesTheRegistry(t *testing.T) {
	cp, err := os.ReadFile("python/filekind/kinds.tsv")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cp, []byte(registryTSV)) {
		t.Fatal("python/filekind/kinds.tsv differs from kinds.tsv; copy kinds.tsv over it")
	}
}

func TestParseRegistryRefuses(t *testing.T) {
	header := "kind\tname\tlane\tgeneration\tencrypted\tvalues\tuse\n"
	row := func(cols ...string) string { return strings.Join(cols, "\t") + "\n" }
	good := row("01", "a", "lane", "1", "no", "sql", "use")
	cases := map[string]string{
		"header":       "kind\tname\n" + good,
		"columns":      header + "01\ta\tlane\t1\tno\tsql\n",
		"upper hex":    header + row("0A", "a", "lane", "1", "no", "sql", "use"),
		"zero byte":    header + row("00", "a", "lane", "1", "no", "sql", "use"),
		"not hex":      header + row("zz", "a", "lane", "1", "no", "sql", "use"),
		"descending":   header + row("02", "a", "lane", "1", "no", "sql", "use") + row("01", "b", "lane", "1", "no", "sql", "use"),
		"repeat byte":  header + good + row("01", "b", "lane", "1", "no", "sql", "use"),
		"repeat name":  header + good + row("02", "a", "lane", "1", "no", "sql", "use"),
		"generation 0": header + row("01", "a", "lane", "0", "no", "sql", "use"),
		"encrypted":    header + row("01", "a", "lane", "1", "maybe", "sql", "use"),
		"values":       header + row("01", "a", "lane", "1", "no", "decimal", "use"),
		"empty lane":   header + row("01", "a", "", "1", "no", "sql", "use"),
	}
	for name, text := range cases {
		if _, err := parseRegistry(text); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if _, err := parseRegistry(header + good); err != nil {
		t.Fatalf("good registry refused: %v", err)
	}
}

func TestApplicationIDAndKindOf(t *testing.T) {
	if got := RunLedger.ApplicationID(); got != 0x4F524220 {
		t.Fatalf("RunLedger.ApplicationID() = %#x", got)
	}
	if got := Fixture.ApplicationID(); got != 0x4F5242FF || got < 0 {
		t.Fatalf("Fixture.ApplicationID() = %#x", got)
	}
	if k, ok := KindOf(0x4F524210); !ok || k != AuditSegment {
		t.Fatalf("KindOf(0x4F524210) = %v, %v", k, ok)
	}
	for _, id := range []int32{0, 0x12345678, 0x4F524142, -1} {
		if _, ok := KindOf(id); ok {
			t.Fatalf("KindOf(%#x) accepted", id)
		}
	}
	if Kind(0xEE).String() != "kind 0xee" || RunLedger.String() != "runLedger" {
		t.Fatalf("String: %q %q", Kind(0xEE).String(), RunLedger.String())
	}
}
