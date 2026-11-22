package filekind

import (
	"errors"
	"fmt"
)

// Header refusals. Check returns each wrapped in a *HeaderError carrying what
// the file holds and what the caller asked for; errors.Is matches the sentinel.
// The Python helper raises an exception of the same name for each (SPEC.md,
// "Refusals"), and the golden vectors name the outcome the same way.
var (
	// ErrUnstamped: the file's application_id is zero, so it names no kind.
	ErrUnstamped = errors.New("filekind: file carries no kind")
	// ErrForeignFile: the application_id is set but outside the 'ORB' prefix,
	// or a stamp would take over a user_version another scheme already set.
	ErrForeignFile = errors.New("filekind: file belongs to another application")
	// ErrUnknownKind: the 'ORB' prefix with a kind byte the registry does not hold.
	ErrUnknownKind = errors.New("filekind: kind is not registered")
	// ErrWrongKind: a registered kind other than the one the caller expects.
	ErrWrongKind = errors.New("filekind: file is a different kind")
	// ErrNoGeneration: the right kind with a user_version below 1.
	ErrNoGeneration = errors.New("filekind: file carries no format generation")
	// ErrNewerGeneration: a format generation newer than the caller reads.
	ErrNewerGeneration = errors.New("filekind: format generation is newer than this reader")
	// ErrOlderGeneration: a format generation older than the caller reads.
	ErrOlderGeneration = errors.New("filekind: format generation is older than this reader")
)

// Writer refusals.
var (
	// ErrGenerationBackwards: a stamp would lower a file's format generation.
	ErrGenerationBackwards = errors.New("filekind: stamp would lower the format generation")
	// ErrStampNotHeld: the engine accepted a stamp and then read back other values.
	ErrStampNotHeld = errors.New("filekind: engine did not hold the stamp")
	// ErrCheckpointBusy: the checkpoint could not move every WAL frame into the
	// file, so the header on disk may still be the old one.
	ErrCheckpointBusy = errors.New("filekind: checkpoint did not complete")
	// ErrUnreadableHeader: the engine answered a header pragma with something
	// other than one whole number.
	ErrUnreadableHeader = errors.New("filekind: header pragma did not return one integer")
	// ErrNotPlainFile: ReadFileHeader found neither an empty file nor the
	// plaintext SQLite magic, so its header can only be read through the engine.
	ErrNotPlainFile = errors.New("filekind: not a plaintext SQLite file")
)

// Digest and value refusals.
var (
	// ErrInvalidArgument: a caller passed an unregistered kind, a generation
	// below 1, or an empty or malformed row set.
	ErrInvalidArgument = errors.New("filekind: invalid argument")
	// ErrNotBaseTable: a row set names a view, a virtual table or nothing.
	ErrNotBaseTable = errors.New("filekind: row set does not name a base table")
	// ErrKeyNotUnique: two rows of a row set share a key, so their order is
	// not fixed by the key.
	ErrKeyNotUnique = errors.New("filekind: row set key is not unique")
	// ErrValueType: a value's Go type disagrees with the storage class typeof
	// reported, or the storage class is not one of the five.
	ErrValueType = errors.New("filekind: value does not match its storage class")
	// ErrInvalidText: a TEXT value is not valid UTF-8.
	ErrInvalidText = errors.New("filekind: text value is not valid UTF-8")
	// ErrBinaryNumber: a base-3 kind holds an INTEGER or REAL value.
	ErrBinaryNumber = errors.New("filekind: base-3 kind holds a binary number")
)

// Want is what a reader expects of a file: its kind and the generations its
// code reads. Oldest 0 reads every generation from 1 up to Newest.
type Want struct {
	Kind   Kind
	Newest int32
	Oldest int32
}

// HeaderError is a header refusal with the values that caused it. It carries
// no path or data-source name, only the two header numbers.
type HeaderError struct {
	Err   error
	Found Header
	Want  Want
}

func (e *HeaderError) Error() string {
	return fmt.Sprintf("%v: application_id 0x%08x user_version %d, want %s generation %d to %d",
		e.Err, uint32(e.Found.ApplicationID), e.Found.UserVersion, e.Want.Kind, e.Want.oldest(), e.Want.Newest)
}

func (e *HeaderError) Unwrap() error { return e.Err }

func (w Want) oldest() int32 {
	if w.Oldest == 0 {
		return 1
	}
	return w.Oldest
}
