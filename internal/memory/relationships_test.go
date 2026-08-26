package memory

import (
	"errors"
	"testing"
)

func TestValidateRelationshipShapeVocabulary(t *testing.T) {
	t.Parallel()
	chunk := ObjectRef{Kind: ObjectKindChunk, ID: string(testChunkID)}
	otherChunk := ObjectRef{Kind: ObjectKindChunk, ID: string(testOtherChunk)}
	entry := ObjectRef{Kind: ObjectKindEntry, ID: string(testEntryID)}
	otherEntry := ObjectRef{Kind: ObjectKindEntry, ID: string(testOtherEntry)}
	valid := []struct {
		kind           LinkKind
		source, target ObjectRef
	}{
		{LinkKindRelatedTo, chunk, entry}, {LinkKindPartOf, entry, chunk},
		{LinkKindPartOf, chunk, otherChunk}, {LinkKindRequires, entry, chunk},
		{LinkKindAlternativeTo, entry, otherEntry}, {LinkKindAlternativeTo, chunk, otherChunk},
		{LinkKindAppliesTo, entry, chunk}, {LinkKindSupersedes, entry, otherEntry},
		{LinkKindContradicts, entry, otherEntry}, {LinkKindCausedBy, entry, otherEntry},
		{LinkKindSupportedBy, entry, chunk}, {LinkKindDerivedFrom, chunk, entry},
	}
	for _, test := range valid {
		if err := ValidateRelationshipShape(test.kind, test.source, test.target); err != nil {
			t.Errorf("%s %s->%s rejected: %v", test.kind, test.source.Kind, test.target.Kind, err)
		}
	}
}

func TestValidateRelationshipShapeRejectsReversedOrWrongArity(t *testing.T) {
	t.Parallel()
	chunk := ObjectRef{Kind: ObjectKindChunk, ID: string(testChunkID)}
	otherChunk := ObjectRef{Kind: ObjectKindChunk, ID: string(testOtherChunk)}
	entry := ObjectRef{Kind: ObjectKindEntry, ID: string(testEntryID)}
	invalid := []struct {
		kind           LinkKind
		source, target ObjectRef
	}{
		{LinkKindPartOf, chunk, entry}, {LinkKindAlternativeTo, chunk, entry},
		{LinkKindAppliesTo, chunk, entry}, {LinkKindSupersedes, chunk, otherChunk},
		{LinkKindContradicts, entry, chunk}, {LinkKindCausedBy, chunk, entry},
		{LinkKindSupportedBy, chunk, entry}, {LinkKindRelatedTo, entry, entry},
	}
	for _, test := range invalid {
		if err := ValidateRelationshipShape(test.kind, test.source, test.target); !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("%s %s->%s error = %v, want ErrInvalidRecord", test.kind, test.source.Kind, test.target.Kind, err)
		}
	}
}
