package ocr

import "testing"

// The captions on the results screen of a Japanese console, as seen live
// (2026-11-17/18): some use the Japanese romanisation (MURABITO, PURIN,
// KOOPA, PACKUN FLOWER), two-line ones arrive with a line break.
func TestMatchCharacterReadsJapaneseConsoleCaptions(t *testing.T) {
	cases := map[string]string{
		"LUCINA": "lucina", "PURIN": "jigglypuff", "IKE": "ike", "MURABITO": "villager",
		"BANJO\n& KAZOOIE": "banjo_kazooie", "BYLETH": "byleth", "YOUNG LINK": "young_link",
		"TERRY": "terry", "SEPHIROTH": "sephiroth", "SORA": "sora", "Wii Fit\nTRAINER": "wii_fit_trainer",
		"DONKEY\nKONG": "donkey_kong", "LITTLE MAC": "little_mac", "MARIO": "mario",
		"PACKUN\nFLOWER": "piranha_plant", "KOOPA": "bowser", "ZERO SUIT\nSAMUS": "zero_suit_samus",
		"LINK": "link", "WARIO": "wario", "YOSHI": "yoshi", "MARTH": "marth", "SIMON": "simon",
		"KIRBY": "kirby", "ZELDA": "zelda", "HERO": "hero", "MINMIN": "min_min", "SAMUS": "samus",
		"RYU": "ryu", "KAZUYA": "kazuya", "HIKARI": "pyra_mythra",
	}
	for text, want := range cases {
		got, ok := MatchCharacter(text)
		if !ok || got != want {
			t.Errorf("%q: got %q (%v), want %q", text, got, ok, want)
		}
	}
}

func TestPlausibleName(t *testing.T) {
	for _, s := range []string{"はるか", "ぎんぴか", "レンジでチンたなはわ", "つゆだく", "Taro", "ゆげどおしをまたあか"} {
		if !PlausibleName(s) {
			t.Errorf("%q should be a plausible name", s)
		}
	}
	for _, s := range []string{"ア.ン", "a", "、、、るか", "◆◆", ""} {
		if PlausibleName(s) {
			t.Errorf("%q should not become a player", s)
		}
	}
}

func TestMatchNameFoldsWhatSmallTagsLose(t *testing.T) {
	known := []string{"ぎんぴか", "つゆだく", "はるか", "レンジでチンたなはわ", "ゆげどおしをまたあか"}
	for text, want := range map[string]string{
		"きんひか": "ぎんぴか", "つゆたく": "つゆだく", "っゆだく": "つゆだく",
		"ゆけとおしをまたあか": "ゆげどおしをまたあか", "レンシでチンたなはわ": "レンジでチンたなはわ",
	} {
		if got := MatchName(text, known); got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
	if got := MatchName("たろう", known); got != "" {
		t.Errorf("a different name must not match: %q", got)
	}
}

// Readings OCR made of the narrow 7- and 8-player captions on 2026-11-18:
// the garbled ones must not be matched to anyone, the merely misspelt ones
// still must be.
func TestMatchCharacterRejectsGarbledCaptions(t *testing.T) {
	for _, s := range []string{"◎R", "AC", "A◎", "Rー◎", "町礼EMAC", "盟U盟引す", "2E55", "uNK", "JS◎RA"} {
		if got, ok := MatchCharacter(s); ok {
			t.Errorf("%q matched %q; a garbled reading must not name a fighter", s, got)
		}
	}
	for s, want := range map[string]string{"SEPHIRTH": "sephiroth", "PONKEVKONG": "donkey_kong", "DADSY": "daisy", "SAMUSj": "samus", "WiiFitTRAINERI": "wii_fit_trainer", "IKAZUYAI": "kazuya", `\@EACH`: "peach", "sORA": "sora"} {
		if got, ok := MatchCharacter(s); !ok || got != want {
			t.Errorf("%q: got %q (%v), want %q", s, got, ok, want)
		}
	}
}
