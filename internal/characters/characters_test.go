package characters

import (
	"strings"
	"testing"
)

func TestResolveAcceptsJapaneseEnglishAndKeys(t *testing.T) {
	for in, want := range map[string]string{
		"マリオ": "mario", "mario": "mario", "MARIO": "mario", " Mario ": "mario",
		"リンク": "link", "link": "link",
		"min_min": "min_min", "MINMIN": "min_min", "ミェンミェン": "min_min",
		"サムス": "samus", "samus": "samus",
		"カズヤ": "kazuya", "kazuya": "kazuya",
	} {
		ch, ok := Resolve(in)
		if !ok || ch.Key != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", in, ch.Key, ok, want)
		}
	}
	for _, in := range []string{"", "   ", "???", "のんびり", "mario2"} {
		if ch, ok := Resolve(in); ok {
			t.Errorf("Resolve(%q) accepted as %q", in, ch.Key)
		}
	}
}

func TestRosterIsConsistent(t *testing.T) {
	if len(All) < 80 {
		t.Fatalf("only %d fighters", len(All))
	}
	keys := map[string]bool{}
	for _, ch := range All {
		if ch.Key == "" || ch.NameJA == "" {
			t.Fatalf("fighter without key or name: %+v", ch)
		}
		if ch.Key != strings.ToLower(ch.Key) || strings.ContainsAny(ch.Key, " /:") {
			t.Fatalf("key %q is not a plain lower-case identifier", ch.Key)
		}
		if keys[ch.Key] {
			t.Fatalf("duplicate key %q", ch.Key)
		}
		keys[ch.Key] = true
		// every fighter resolves from its own key and its Japanese name
		for _, in := range []string{ch.Key, ch.NameJA} {
			got, ok := Resolve(in)
			if !ok || got.Key != ch.Key {
				t.Fatalf("Resolve(%q) = %q, %v; want %q", in, got.Key, ok, ch.Key)
			}
		}
		if got, ok := ByKey(ch.Key); !ok || got.NameJA != ch.NameJA {
			t.Fatalf("ByKey(%q) = %+v, %v", ch.Key, got, ok)
		}
	}
	if _, ok := ByKey("nobody"); ok {
		t.Fatal("ByKey accepted an unknown key")
	}
}
