package knowledge

func IsSymmetricLinkKind(kind LinkKind) bool {
	switch kind {
	case LinkKindRelatedTo, LinkKindAlternativeTo, LinkKindContradicts:
		return true
	default:
		return false
	}
}

// ValidateRelationshipShape enforces the endpoint arity and direction defined by the
// initial relationship vocabulary. Existence and cross-chunk authorization belong to the
// service because they require canonical reads and actor policy.
func ValidateRelationshipShape(kind LinkKind, source, target ObjectRef) error {
	if kind == LinkKindUnspecified || !kind.IsALinkKind() {
		return invalid("kind", "is not a known link kind")
	}
	if err := validateEndpoint("source", source); err != nil {
		return err
	}
	if err := validateEndpoint("target", target); err != nil {
		return err
	}
	if source == target {
		return invalid("target", "must differ from source")
	}
	switch kind {
	case LinkKindRelatedTo, LinkKindRequires, LinkKindDerivedFrom:
		return nil
	case LinkKindAlternativeTo:
		if source.Kind != target.Kind {
			return invalid("target.kind", "alternative_to endpoints must have the same object kind")
		}
	case LinkKindPartOf:
		if target.Kind != ObjectKindChunk {
			return invalid("target.kind", "part_of must point to a chunk")
		}
	case LinkKindAppliesTo:
		if source.Kind != ObjectKindEntry {
			return invalid("source.kind", "applies_to must start at an entry")
		}
	case LinkKindSupersedes, LinkKindContradicts, LinkKindCausedBy:
		if source.Kind != ObjectKindEntry || target.Kind != ObjectKindEntry {
			return invalid("source", kind.String()+" requires two entry endpoints")
		}
	case LinkKindSupportedBy:
		if source.Kind != ObjectKindEntry {
			return invalid("source.kind", "supported_by must start at an entry")
		}
	}
	return nil
}
