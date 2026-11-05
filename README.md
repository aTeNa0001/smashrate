# smashrate

スマブラ SP のローカル対戦向けレーティング・戦績システム。ミニPC + キャプチャボードで
リザルト画面を自動で読み取り、Slack と Web からレート・勝率・使用キャラ・チーム分け提案を
参照できます。単一の Go バイナリで動き、外部サービス・クラウド API は使いません。

## ミニPCへの導入（これだけ）

管理者 PowerShell（スタートを右クリック →「ターミナル（管理者）」）に次の 2 行を貼る。

```powershell
Set-ExecutionPolicy -Scope Process Bypass -Force
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/aTeNa0001/smashrate/main/deploy/setup.ps1))) -FromSource
```

ffmpeg・cloudflared・Go・Git の導入、ソース取得とビルド、`C:\smashrate` への配置、
設定ファイルの雛形作成、ログオン時の自動起動と日次バックアップの登録まで行い、最後に
`doctor`（前提条件チェック）の結果を表示します。更新したいときも同じ 2 行をもう一度実行します。

終わったら:

1. ブラウザで http://localhost:8080 を開く（ランキング画面が出れば本体は動いている）
2. `C:\smashrate\smashrate.toml` を編集（キャプチャのデバイス名、パスワード、Slack）
3. `cd C:\smashrate; .\smashrate.exe doctor` で足りないものを確認し、FAIL が消えるまで直す
4. `schtasks /End /TN smashrate; schtasks /Run /TN smashrate` で再起動

### doctor が見るもの

設定ファイル、データフォルダの書き込み、DB、空き容量、Web ポート、ログイン設定、
Smart App Control の状態、ffmpeg、キャプチャデバイスの存在、実際にフレームが取れるか、
リザルト画面テンプレートの有無、名前タグ OCR の動作、Slack トークンの認証、cloudflared。
FAIL には直し方を併記します。

## 機能

- **レーティング**: OpenSkill（Plackett–Luce）を純 Go で実装。1on1・乱闘（最大 8 人）・チーム戦（人数不均衡や同率も可）を同じ式で扱う。
  総合レートに加え、形式別（1on1 / 乱闘 / チーム）とキャラ別のサブレートを並行して更新。
- **戦績**: 勝率、連勝、平均順位、使用キャラとキャラ別勝率、1on1 の対戦相手別成績、レート推移。
- **チーム分け**: メンバー（最大 8 人）とチーム数（2〜4）を指定すると、総合レートが釣り合う分け方を勝率予測付きで 3 案提示。人数差のある分け方（7 人を 4/2/1 など）も許容でき、その場合は少ない側に強い人を寄せて釣り合わせる。
- **自動記録**: キャプチャボードの映像からリザルト画面を検出し、名前タグ・キャラ・順位を画像照合で読んで記録。初めて見る名前タグだけ OCR（Windows 標準、追加インストール不要）で読んでプレイヤーを自動登録するので、新人が来ても事前登録は不要（詳細は [docs/VISION.md](docs/VISION.md)）。
- **インターフェース**: Web UI（LAN 内）、JSON API、Slack スラッシュコマンド（Socket Mode なので公開 URL 不要）、CLI。
- **訂正**: 試合の編集・削除、名前の修正・統合を行うと、全履歴からレートを再計算。

## 自動記録を動かすまで

1. Switch のドック → キャプチャボード → テレビと繋ぎ、キャプチャボードの USB をミニPCの USB 3.0 へ。
2. `.\smashrate.exe vision devices` でデバイス名を確認し、`smashrate.toml` の `[vision]` に `enabled = true` と `input = "video=<名前>"` を書く。
3. リザルト画面が出ている時に `.\smashrate.exe vision snapshot -out shot.png` で 1 枚保存し、`docs/VISION.md` の手順で領域を合わせて `vision teach` を実行。
4. 以後はリザルト画面が出るたびに自動で記録され、Slack に通知される。

## Slack

1. https://api.slack.com/apps でアプリを作成し、Socket Mode を有効化（App-Level Token: `connections:write`）。
2. Slash Commands に `/smash` を追加。Bot Token Scopes に `commands`, `chat:write`。
3. ワークスペースにインストールし、`smashrate.toml` の `[slack]` にトークンを書いて `enabled = true`。

```
/smash rank                        ランキング
/smash stats 太郎                  成績
/smash record 太郎:マリオ > 花子:リンク   1on1（太郎の勝ち）
/smash record A > B > C > D        乱闘の順位
/smash record A+B > C+D            チーム戦
/smash balance 2 A B C D E F       チーム分け（末尾に uneven で人数差を許容）
/smash fix 12 太郎 ルイージ        キャラ修正
/smash learn name-5678 太郎        OCR で読めなかった名前タグを手動登録
/smash rename Hanak0 Hanako        OCR の読み間違いを修正
/smash merge Hanak0 Hanako         二重登録を統合して再計算
```

## 外部公開（Cloudflare Tunnel + Access）と簡易ログイン

`[server.auth]` にパスワードを設定すると、記録・編集・削除にログインが必要になります
（閲覧も守るなら `read_requires_login = true`）。スクリプトからは `Authorization: Bearer <api_token>` で
書き込めます。ポート開放なしで公開する手順と、Cloudflare Access でメンバーを絞る設定は
[docs/DEPLOY.md](docs/DEPLOY.md) を参照。

## 開発者向け

```
go build -o smashrate ./cmd/smashrate
go test ./...
```

```
cmd/smashrate         CLI（serve / doctor / record / rank / balance / vision ...）
internal/rating       OpenSkill Plackett–Luce（依存なし）
internal/balance      チーム分け探索（全列挙 or 山登り）
internal/core         SQLite スキーマ、試合記録、レート適用、再計算、統計
internal/characters   ファイター一覧と名前解決（日本語・英語・略称）
internal/parse        "A:mario > B+C" 記法のパーサ（Slack と CLI で共用）
internal/httpapi      Web UI（html/template）と JSON API、簡易ログイン
internal/slackbot     Socket Mode のスラッシュコマンド
internal/vision       ffmpeg 入力、dHash、リザルト画面検出、学習、監視ループ
internal/ocr          名前タグ OCR（Windows 標準 / 外部コマンド）と名前のあいまい一致
docs/VISION.md        画像認識の方式比較、採用理由、調整手順
docs/DEPLOY.md        ミニPC セットアップ、Cloudflare Tunnel + Access
```

### JSON API

| Method | Path | 内容 |
|---|---|---|
| GET | /api/leaderboard?scope=overall\|mode:1v1\|char:mario | ランキング（Web の `/?scope=` も同じ値） |
| GET | /api/players/{name} | 個人成績 |
| GET | /api/matches?limit=&offset= | 対戦履歴 |
| POST | /api/matches | `{"entries":[{"player":"A","character":"mario","team":1,"rank":1},...]}` |
| PUT | /api/matches/{id} | 参加者・順位・キャラを差し替えて再計算（本文は POST と同じ） |
| DELETE | /api/matches/{id} | 削除して再計算 |
| POST | /api/balance | `{"members":["A","B","C","D"],"teams":2,"allow_uneven":false}` |

### レート表示

内部は μ（初期 25）と σ（初期 25/3）。表示は `1500 + 25 × (μ − 3σ)` で、新規プレイヤーは 1500。
