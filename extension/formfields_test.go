package extension_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/usestring/gate-inbox/extension"
)

// former adds fields to the new-session form and records the form values
// each launch hands it.
type former struct {
	id     string
	fields []extension.FormField
	panics bool
	seen   []map[string]string
}

func (f *former) Descriptor() extension.Descriptor { return extension.Descriptor{ID: f.id} }
func (f *former) Configure(extension.Config) error { return nil }
func (f *former) SessionFormFields() []extension.FormField {
	if f.panics {
		panic("no fields today")
	}
	return f.fields
}
func (f *former) LaunchEnv(_ context.Context, launch extension.Launch) (map[string]string, error) {
	f.seen = append(f.seen, launch.Form)
	return nil, nil
}

// Fields are qualified by their extension and made whole; a malformed one,
// or every field of an extension that panics, is left off and named.
func TestFormFieldsAreQualifiedAndMadeWhole(t *testing.T) {
	hooks := sessionHooks(t,
		&former{id: "ext1", fields: []extension.FormField{
			{Key: "items", Kind: extension.FormToggle, Default: extension.FormOn},
			{Key: "level", Label: "who", Kind: extension.FormChoice, Options: []string{"a", "b"}, Default: "z"},
			{Key: "", Kind: extension.FormToggle},
			{Key: "a/b", Kind: extension.FormToggle},
			{Key: "empty", Kind: extension.FormChoice},
			{Key: "odd", Kind: "slider"},
			{Key: "items", Kind: extension.FormToggle},
		}},
		&former{id: "tally", panics: true},
		&former{id: "extra", fields: []extension.FormField{{Key: "items", Kind: extension.FormToggle}}},
	)
	fields, err := hooks.FormFields()
	want := []extension.FormField{
		{Key: "ext1/items", Label: "items", Kind: extension.FormToggle, Options: []string{"off", "on"}, Default: "on"},
		{Key: "ext1/level", Label: "who", Kind: extension.FormChoice, Options: []string{"a", "b"}, Default: "a"},
		{Key: "extra/items", Label: "items", Kind: extension.FormToggle, Options: []string{"off", "on"}, Default: "off"},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %+v\nwant %+v", fields, want)
	}
	for _, named := range []string{`"a/b"`, `"empty"`, `"odd"`, `given twice`, `extension "tally"`} {
		if err == nil || !strings.Contains(err.Error(), named) {
			t.Errorf("error %v does not name %s", err, named)
		}
	}
	var none *extension.SessionHooks
	if fields, err := none.FormFields(); fields != nil || err != nil {
		t.Fatalf("nil hooks: %v %v", fields, err)
	}
}

// Each contributor is handed only its own fields' values, under its own
// keys, and a launch not from the form hands none.
func TestLaunchHandsEachExtensionItsOwnFormValues(t *testing.T) {
	ext1 := &former{id: "ext1"}
	extra := &former{id: "extra"}
	tally := &former{id: "tally"}
	hooks := sessionHooks(t, ext1, extra, tally)
	form := map[string]string{"ext1/items": "on", "extra/items": "off", "extra/level": "b"}
	if _, err := hooks.LaunchEnv(context.Background(), extension.Launch{Reason: extension.LaunchSpawn, Form: form}); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.LaunchEnv(context.Background(), extension.Launch{Reason: extension.LaunchSpawn}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ext  *former
		want map[string]string
	}{
		{ext1, map[string]string{"items": "on"}},
		{extra, map[string]string{"items": "off", "level": "b"}},
		{tally, nil},
	} {
		if len(tc.ext.seen) != 2 || !reflect.DeepEqual(tc.ext.seen[0], tc.want) || tc.ext.seen[1] != nil {
			t.Errorf("%s saw %v, want %v then nothing", tc.ext.id, tc.ext.seen, tc.want)
		}
	}
}
