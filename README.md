# smashrate

スマブラ SP のローカル対戦向けレーティング・戦績システム。ミニPC + キャプチャボードで
リザルト画面を自動で読み取り、Slack と Web からレート・勝率・使用キャラ・チーム分け提案を
参照できます。単一の Go バイナリで動き、外部サービス・クラウド API は使いません。

## 機能

- **レーティング**: OpenSkill（Plackett–Luce）を純 Go で実装。1on1・乱闘（最大 8 人）・チーム戦（人数不均衡や同率も可）を同じ式で扱う。
  総合レートに加え、形式別（1on1 / 乱闘 / チーム）とキャラ別のサブレートを並行して更新。
- **戦績**: 勝率、連勝、平均順位、使用キャラとキャラ別勝率、1on1 の対戦相手別成績、レート推移。
- **チーム分け**: メンバー（最大 8 人）とチーム数（2〜4）を指定すると、総合レートが釣り合う分け方を勝率予測付きで 3 案提示。人数差のある分け方（7 人を 4/2/1 など）も許容でき、その場合は少ない側に強い人を寄せて釣り合わせる。
- **自動記録**: キャプチャボードの映像からリザルト画面を検出し、名前タグ・キャラ・順位をすべて画像照合で読んで記録（OCR 不要、ポート設定も不要。未知のタグやキャラは Slack から教えて育てる。詳細は [docs/VISION.md](docs/VISION.md)）。
- **インターフェース**: Web UI（LAN 内）、JSON API、Slack スラッシュコマンド（Socket Mode なので公開 URL 不要）、CLI。
- **訂正**: 試合の削除やキャラ修正を行うと、全履歴からレートを再計算。

## セットアップ

```
go build -o smashrate ./cmd/smashrate
cp smashrate.example.toml smashrate.toml   # 必要な項目を編集
./smashrate serve                          # http://localhost:8080
```

Windows のミニPC では `go build -o smashrate.exe ./cmd/smashrate` のあと、タスクスケジューラで
ログオン時に `smashrate.exe serve` を起動すれば常駐します。

### Slack

1. https://api.slack.com/apps でアプリを作成し、Socket Mode を有効化（App-Level Token: `connections:write`）。
2. Slash Commands に `/smash` を追加。Bot Token Scopes に `commands`, `chat:write`。
3. ワークスペースにインストールし、`smashrate.toml` の `[slack]` にトークンを書いて `enabled = true`。

```
/smash rank                        ランキング
/smash stats 太郎                  成績
/smash record 太郎:マリオ > 花子:リンク   1on1（太郎の勝ち）
/smash record A > B > C > D        乱闘の順位
/smash record A+B > C+D            チーム戦
/smash balance 2 A B C D E F       チーム分け
/smash fix 12 太郎 ルイージ        キャラ修正
/smash learn char-1234 ピカチュウ  未知のキャラ画像を学習
/smash learn name-5678 太郎        未知の名前タグを学習（以後そのタグの人を自動判別）
/smash ports P1=太郎               タグ未学習時の応急処置
```

### 外部公開（Cloudflare Tunnel + Access）と簡易ログイン

`[server.auth]` にパスワードを設定すると、記録・削除・ポート設定にログインが必要になります
（閲覧も守るなら `read_requires_login = true`）。スクリプトからは `Authorization: Bearer <api_token>` で
書き込めます。ポート開放なしで公開する手順と、Cloudflare Access でメンバーを絞る設定は
[docs/DEPLOY.md](docs/DEPLOY.md) を参照。常駐化は `deploy/install-task.ps1`、日次バックアップは `deploy/backup.ps1`。

### キャプチャボード

ffmpeg が必要です（Windows: `winget install Gyan.FFmpeg`）。`smashrate.toml` の `[vision]` を
`enabled = true` にし、`docs/VISION.md` の調整手順でレイアウトとテンプレートを登録してください。

## 構成

```
cmd/smashrate         CLI エントリポイント（serve / record / rank / balance / vision ...）
internal/rating       OpenSkill Plackett–Luce（依存なし）
internal/balance      チーム分け探索（全列挙 or 山登り）
internal/core         SQLite スキーマ、試合記録、レート適用、再計算、統計
internal/characters   ファイター一覧と名前解決（日本語・英語・略称）
internal/parse        "A:mario > B+C" 記法のパーサ（Slack と CLI で共用）
internal/httpapi      Web UI（html/template）と JSON API
internal/slackbot     Socket Mode のスラッシュコマンド
internal/vision       ffmpeg 入力、dHash、リザルト画面検出、学習、監視ループ
docs/VISION.md        OCR と他方式の比較、採用理由、調整手順
```

## JSON API

| Method | Path | 内容 |
|---|---|---|
| GET | /api/leaderboard?scope=overall\|mode:1v1\|char:mario | ランキング（Web の `/?scope=` も同じ値） |
| GET | /api/players/{name} | 個人成績 |
| GET | /api/matches?limit=&offset= | 対戦履歴 |
| POST | /api/matches | `{"entries":[{"player":"A","character":"mario","team":1,"rank":1},...]}` |
| PUT | /api/matches/{id} | 参加者・順位・キャラを差し替えて再計算（本文は POST と同じ） |
| DELETE | /api/matches/{id} | 削除して再計算 |
| POST | /api/balance | `{"members":["A","B","C","D"],"teams":2,"allow_uneven":false}` |
| GET / PUT | /api/ports, /api/ports/{port} | ポート設定 |

## レート表示

内部は μ（初期 25）と σ（初期 25/3）。表示は `1500 + 25 × (μ − 3σ)` で、新規プレイヤーは 1500。

## テスト

```
go test ./...
```
