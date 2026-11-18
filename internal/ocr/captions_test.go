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
