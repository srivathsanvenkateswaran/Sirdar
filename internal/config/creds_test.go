package config

import "testing"

type fakeKC map[string]string

func (f fakeKC) Read(s string) (string, error) {
	v, ok := f[s]
	if !ok {
		return "", &NotFoundError{Ref: "keychain:" + s}
	}
	return v, nil
}

func TestResolve(t *testing.T) {
	r := Resolver{
		Env: func(k string) (string, bool) {
			if k == "ZOHO" {
				return "e-tok", true
			}
			return "", false
		},
		Keychain: fakeKC{"zoho": "k-tok"},
	}
	if v, _ := r.Resolve("env:ZOHO"); v != "e-tok" {
		t.Fatal(v)
	}
	if v, _ := r.Resolve("keychain:zoho"); v != "k-tok" {
		t.Fatal(v)
	}
	if _, err := r.Resolve("env:MISSING"); err == nil {
		t.Fatal("want error")
	}
	if _, err := r.Resolve("keychain:nope"); err == nil {
		t.Fatal("want error")
	}
	if _, err := r.Resolve("literal"); err == nil {
		t.Fatal("literal refs are rejected")
	}
}
