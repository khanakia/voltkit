package appmeta

import "testing"

// TestDBLocation_Valid guards the reason Valid exists: an unrecognised policy
// must be rejected loudly rather than falling through to a default at runtime,
// where it would silently resolve the database to the wrong place.
func TestDBLocation_Valid(t *testing.T) {
	tests := []struct {
		location DBLocation
		want     bool
	}{
		{LocationUserGlobal, true},
		{LocationProjectLocal, true},
		{LocationBoth, true},
		{DBLocation(""), false},
		{DBLocation("global"), false},
		{DBLocation("USER-GLOBAL"), false},
	}

	for _, tt := range tests {
		if got := tt.location.Valid(); got != tt.want {
			t.Errorf("DBLocation(%q).Valid() = %v, want %v", tt.location, got, tt.want)
		}
	}
}

// TestDefault_IsSelfConsistent catches a hand-edited or mis-generated Default
// block before it reaches path resolution.
func TestDefault_IsSelfConsistent(t *testing.T) {
	if !Default.DBLocation.Valid() {
		t.Errorf("Default.DBLocation = %q is not a known policy", Default.DBLocation)
	}
	for _, f := range []struct{ name, value string }{
		{"Name", Default.Name},
		{"Binary", Default.Binary},
		{"EnvPrefix", Default.EnvPrefix},
		{"DirName", Default.DirName},
		{"DBFilename", Default.DBFilename},
		{"DocsURL", Default.DocsURL},
	} {
		if f.value == "" {
			t.Errorf("Default.%s is empty", f.name)
		}
	}
}
