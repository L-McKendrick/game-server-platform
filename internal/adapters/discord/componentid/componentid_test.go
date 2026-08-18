package componentid

import "testing"

func TestRevisionedReferenceRoundTrip(t *testing.T) {
	t.Parallel()
	token := "S_Abcdefghijklmnopqrstuvwx"
	customID, err := New(ActionRefresh, 42, token)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := Parse(customID)
	if err != nil || reference.Action != ActionRefresh || reference.Revision != 42 {
		t.Fatalf("Parse() = %#v, %v", reference, err)
	}
	if reference.Token != token {
		t.Fatalf("token = %q; want %q", reference.Token, token)
	}
}
