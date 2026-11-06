// Package characters holds the Super Smash Bros. Ultimate roster and a
// forgiving name resolver (English, Japanese, common abbreviations).
package characters

import (
	"sort"
	"strings"
)

// Character is one fighter.
type Character struct {
	Key     string // stable identifier, e.g. "mario"
	NameEN  string
	NameJA  string
	Aliases []string
}

// All is the roster in official numbering order (echo fighters follow their base).
var All = []Character{
	{"mario", "Mario", "マリオ", nil},
	{"donkey_kong", "Donkey Kong", "ドンキーコング", []string{"dk", "ドンキー"}},
	{"link", "Link", "リンク", nil},
	{"samus", "Samus", "サムス", nil},
	{"dark_samus", "Dark Samus", "ダークサムス", []string{"ダムス"}},
	{"yoshi", "Yoshi", "ヨッシー", nil},
	{"kirby", "Kirby", "カービィ", []string{"カービー"}},
	{"fox", "Fox", "フォックス", nil},
	{"pikachu", "Pikachu", "ピカチュウ", []string{"ピカ"}},
	{"luigi", "Luigi", "ルイージ", nil},
	{"ness", "Ness", "ネス", nil},
	{"captain_falcon", "Captain Falcon", "キャプテン・ファルコン", []string{"falcon", "ファルコン", "キャプテンファルコン"}},
	{"jigglypuff", "Jigglypuff", "プリン", []string{"puff", "purin"}},
	{"peach", "Peach", "ピーチ", nil},
	{"daisy", "Daisy", "デイジー", nil},
	{"bowser", "Bowser", "クッパ", []string{"koopa"}},
	{"ice_climbers", "Ice Climbers", "アイスクライマー", []string{"ics", "アイクラ"}},
	{"sheik", "Sheik", "シーク", nil},
	{"zelda", "Zelda", "ゼルダ", nil},
	{"dr_mario", "Dr. Mario", "ドクターマリオ", []string{"doc", "ドクマリ", "dr mario"}},
	{"pichu", "Pichu", "ピチュー", nil},
	{"falco", "Falco", "ファルコ", nil},
	{"marth", "Marth", "マルス", nil},
	{"lucina", "Lucina", "ルキナ", nil},
	{"young_link", "Young Link", "こどもリンク", []string{"yink", "こどリン", "子供リンク", "kodomo link"}},
	{"ganondorf", "Ganondorf", "ガノンドロフ", []string{"ganon", "ガノン"}},
	{"mewtwo", "Mewtwo", "ミュウツー", nil},
	{"roy", "Roy", "ロイ", nil},
	{"chrom", "Chrom", "クロム", nil},
	{"mr_game_and_watch", "Mr. Game & Watch", "Mr.ゲーム&ウォッチ", []string{"game and watch", "gw", "g&w", "ゲムヲ", "ゲームウォッチ"}},
	{"meta_knight", "Meta Knight", "メタナイト", []string{"mk", "メタナ"}},
	{"pit", "Pit", "ピット", nil},
	{"dark_pit", "Dark Pit", "ブラックピット", []string{"ブラピ", "black pit"}},
	{"zero_suit_samus", "Zero Suit Samus", "ゼロスーツサムス", []string{"zss", "ゼロサム"}},
	{"wario", "Wario", "ワリオ", nil},
	{"snake", "Snake", "スネーク", nil},
	{"ike", "Ike", "アイク", nil},
	{"pokemon_trainer", "Pokémon Trainer", "ポケモントレーナー", []string{"pokemon trainer", "pt", "ポケトレ", "squirtle", "ivysaur", "charizard", "ゼニガメ", "フシギソウ", "リザードン", "zenigame", "fushigisou", "lizardon"}},
	{"diddy_kong", "Diddy Kong", "ディディーコング", []string{"diddy", "ディディー"}},
	{"lucas", "Lucas", "リュカ", nil},
	{"sonic", "Sonic", "ソニック", nil},
	{"king_dedede", "King Dedede", "デデデ", []string{"dedede", "ddd"}},
	{"olimar", "Olimar", "ピクミン&オリマー", []string{"オリマー", "pikmin & olimar", "alph", "アルフ"}},
	{"lucario", "Lucario", "ルカリオ", nil},
	{"rob", "R.O.B.", "ロボット", []string{"r.o.b", "r.o.b.", "ロボ", "robot"}},
	{"toon_link", "Toon Link", "トゥーンリンク", []string{"tink", "トゥーン", "トゥンリン"}},
	{"wolf", "Wolf", "ウルフ", nil},
	{"villager", "Villager", "むらびと", []string{"村人", "murabito"}},
	{"mega_man", "Mega Man", "ロックマン", []string{"megaman", "ロック", "rockman"}},
	{"wii_fit_trainer", "Wii Fit Trainer", "Wii Fit トレーナー", []string{"wft", "wiifit", "wii fit", "フィットレ", "wiifitトレーナー"}},
	// the results-screen caption uses the Japanese market's English spellings
	// for some fighters (KOOPA, PACKUN FLOWER, HOMURA, ROCKMAN, ...); those
	// are listed as aliases on the fighters above
	{"rosalina", "Rosalina & Luma", "ロゼッタ&チコ", []string{"rosalina", "rosa", "ロゼッタ", "ロゼチコ"}},
	{"little_mac", "Little Mac", "リトル・マック", []string{"mac", "マック", "リトルマック"}},
	{"greninja", "Greninja", "ゲッコウガ", []string{"ゲッコ", "gekkouga"}},
	{"mii_brawler", "Mii Brawler", "Mii 格闘タイプ", []string{"brawler", "格闘", "mii格闘"}},
	{"mii_swordfighter", "Mii Swordfighter", "Mii 剣術タイプ", []string{"swordfighter", "剣術", "mii剣術"}},
	{"mii_gunner", "Mii Gunner", "Mii 射撃タイプ", []string{"gunner", "射撃", "mii射撃"}},
	{"palutena", "Palutena", "パルテナ", nil},
	{"pac_man", "Pac-Man", "パックマン", []string{"pacman", "pac man"}},
	{"robin", "Robin", "ルフレ", []string{"reflet"}},
	{"shulk", "Shulk", "シュルク", nil},
	{"bowser_jr", "Bowser Jr.", "クッパJr.", []string{"bowser jr", "jr", "クッパジュニア", "クッパjr", "koopa jr", "koopa jr."}},
	{"duck_hunt", "Duck Hunt", "ダックハント", []string{"ダクハン"}},
	{"ryu", "Ryu", "リュウ", nil},
	{"ken", "Ken", "ケン", nil},
	{"cloud", "Cloud", "クラウド", nil},
	{"corrin", "Corrin", "カムイ", []string{"kamui"}},
	{"bayonetta", "Bayonetta", "ベヨネッタ", []string{"bayo", "ベヨ"}},
	{"inkling", "Inkling", "インクリング", nil},
	{"ridley", "Ridley", "リドリー", nil},
	{"simon", "Simon", "シモン", nil},
	{"richter", "Richter", "リヒター", nil},
	{"king_k_rool", "King K. Rool", "キングクルール", []string{"k rool", "krool", "クルール"}},
	{"isabelle", "Isabelle", "しずえ", []string{"shizue"}},
	{"incineroar", "Incineroar", "ガオガエン", []string{"gaogaen"}},
	{"piranha_plant", "Piranha Plant", "パックンフラワー", []string{"plant", "パックン", "packun flower", "packun"}},
	{"joker", "Joker", "ジョーカー", nil},
	{"hero", "Hero", "勇者", []string{"yuusha"}},
	{"banjo_kazooie", "Banjo & Kazooie", "バンジョー&カズーイ", []string{"banjo", "バンカズ", "バンジョー"}},
	{"terry", "Terry", "テリー", nil},
	{"byleth", "Byleth", "ベレト/ベレス", []string{"ベレト", "ベレス"}},
	{"min_min", "Min Min", "ミェンミェン", []string{"minmin", "min min"}},
	{"steve", "Steve", "スティーブ", []string{"alex", "アレックス", "zombie", "enderman", "ゾンビ", "エンダーマン"}},
	{"sephiroth", "Sephiroth", "セフィロス", []string{"セフィ"}},
	{"pyra_mythra", "Pyra / Mythra", "ホムラ/ヒカリ", []string{"pyra", "mythra", "ホムラ", "ヒカリ", "ホムヒカ", "homura", "hikari"}},
	{"kazuya", "Kazuya", "カズヤ", []string{"一八"}},
	{"sora", "Sora", "ソラ", nil},
}

var byKey = map[string]Character{}
var byAlias = map[string]Character{}

func norm(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	r := strings.NewReplacer(" ", "", "_", "", "-", "", ".", "", "　", "", "・", "", "&", "and")
	return r.Replace(s)
}

func init() {
	for _, c := range All {
		byKey[c.Key] = c
		byAlias[norm(c.Key)] = c
		byAlias[norm(c.NameEN)] = c
		byAlias[norm(c.NameJA)] = c
		for _, a := range c.Aliases {
			byAlias[norm(a)] = c
		}
	}
}

// ByKey looks up a character by its stable key.
func ByKey(key string) (Character, bool) {
	c, ok := byKey[key]
	return c, ok
}

// Resolve maps free text (English, Japanese, abbreviation) to a character.
// It tries an exact alias match first, then a unique prefix match.
func Resolve(text string) (Character, bool) {
	n := norm(text)
	if n == "" {
		return Character{}, false
	}
	if c, ok := byAlias[n]; ok {
		return c, true
	}
	var hits []Character
	seen := map[string]bool{}
	for alias, c := range byAlias {
		if strings.HasPrefix(alias, n) && !seen[c.Key] {
			seen[c.Key] = true
			hits = append(hits, c)
		}
	}
	if len(hits) == 1 {
		return hits[0], true
	}
	return Character{}, false
}

// Keys returns all character keys sorted.
func Keys() []string {
	out := make([]string, 0, len(All))
	for _, c := range All {
		out = append(out, c.Key)
	}
	sort.Strings(out)
	return out
}
