package subjectintel

import "testing"

func TestValidateSubjectRequiresTypedSourceAndConfidence(t *testing.T) {
	for _, tc := range []CreateSubject{{Type: "DOMAIN", Value: "example.org", Source: "OSINT", Confidence: .8}, {Type: "APK", Value: "sha256:abc", Source: "USER", Confidence: 1}} {
		if err := validateSubject(tc); err != nil {
			t.Fatal(err)
		}
	}
	if validateSubject(CreateSubject{Type: "PERSON", Value: "x", Source: "USER", Confidence: 1}) == nil {
		t.Fatal("unsupported type accepted")
	}
	if validateSubject(CreateSubject{Type: "IP", Value: "1.1.1.1", Source: "OSINT", Confidence: 1.2}) == nil {
		t.Fatal("invalid confidence accepted")
	}
}
