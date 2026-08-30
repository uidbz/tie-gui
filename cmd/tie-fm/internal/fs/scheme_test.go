package fs

import "testing"

func TestSchemeOf(t *testing.T) {
	cases := map[string]string{
		"/home/johan":      "file",
		"file:/home/johan": "file",
		"":                 "file",
		"tie:/music":       "tie",
		"tie:":             "tie",
		"mtp:/12-3/65537":  "mtp",
		"mtp:":             "mtp",
	}
	for in, want := range cases {
		if got := SchemeOf(in); got != want {
			t.Errorf("SchemeOf(%q) = %q, want %q", in, got, want)
		}
		if IsLocal(in) != (want == "file") {
			t.Errorf("IsLocal(%q) = %v, want %v", in, IsLocal(in), want == "file")
		}
		if IsTie(in) != (want == "tie") {
			t.Errorf("IsTie(%q) = %v, want %v", in, IsTie(in), want == "tie")
		}
	}
}

type fakeFS struct{ scheme string }

func (f fakeFS) Scheme() string                    { return f.scheme }
func (f fakeFS) List(string) ([]Entry, error)      { return nil, nil }
func (f fakeFS) Materialize(Entry) (string, error) { return "", nil }

func TestRegistryFor(t *testing.T) {
	local := fakeFS{"file"}
	tie := fakeFS{"tie"}
	mtp := fakeFS{"mtp"}
	r := NewRegistry(local, tie)
	r.SetMTP(mtp)

	cases := map[string]string{
		"/tmp/x":     "file",
		"tie:/music": "tie",
		"mtp:/12-3":  "mtp",
	}
	for path, want := range cases {
		if got := r.For(path).Scheme(); got != want {
			t.Errorf("Registry.For(%q).Scheme() = %q, want %q", path, got, want)
		}
	}
}
