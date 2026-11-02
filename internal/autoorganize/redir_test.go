package autoorganize

import (
	"reflect"
	"testing"
)

func TestParseContentRedirCPages(t *testing.T) {
	ci := ParseContent([]byte(`{"fileType":"pdf","pageCount":3,"formatVersion":2,"cPages":{"pages":[
		{"id":"a","idx":{"value":"aa"},"redir":{"value":0}},
		{"id":"b","idx":{"value":"ab"}},
		{"id":"c","idx":{"value":"ac"},"redir":{"value":1}}]}}`))
	want := map[string]int{"a": 0, "b": -1, "c": 1}
	if !reflect.DeepEqual(ci.Redir, want) {
		t.Fatalf("Redir = %v, want %v", ci.Redir, want)
	}
	if ci.ParseVersion != contentParseVersion {
		t.Fatal("parse version")
	}
}

func TestParseContentNoRedirKeyNoMap(t *testing.T) {
	ci := ParseContent([]byte(`{"fileType":"pdf","pageCount":1,"cPages":{"pages":[{"id":"a","idx":{"value":"aa"}}]}}`))
	if ci.Redir != nil {
		t.Fatalf("Redir = %v, want nil", ci.Redir)
	}
}

func TestParseContentRedirV1(t *testing.T) {
	ci := ParseContent([]byte(`{"fileType":"pdf","pageCount":3,"formatVersion":1,"pages":["z","a","m"],"redirectionPageMap":[0,-1,2]}`))
	want := map[string]int{"z": 0, "a": -1, "m": 2}
	if !reflect.DeepEqual(ci.Redir, want) {
		t.Fatalf("Redir = %v, want %v", ci.Redir, want)
	}
	for _, short := range []string{`"redirectionPageMap":[0,1]`, `"x":1`} {
		ci = ParseContent([]byte(`{"fileType":"pdf","pageCount":3,"formatVersion":1,"pages":["z","a","m"],` + short + `}`))
		if ci.Redir != nil {
			t.Fatalf("%s: Redir = %v, want nil", short, ci.Redir)
		}
	}
}
