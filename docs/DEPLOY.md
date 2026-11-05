# ミニPCのセットアップと公開手順

## 0. 最短セットアップ（Windows ミニPC）

Go のビルド環境は不要。管理者 PowerShell で次を実行すると、ffmpeg と cloudflared の導入、
最新リリースの smashrate.exe のダウンロード、設定ファイルの雛形作成、ログオン時起動の
タスク登録、日次バックアップのタスク登録まで一度に行う。

```powershell
Set-ExecutionPolicy -Scope Process Bypass -Force
irm https://github.com/aTeNa0001/smashrate/releases/latest/download/setup.ps1 | iex
```

インストール先は `C:\smashrate`。終わったら `C:\smashrate\smashrate.toml` を編集して、
`schtasks /End /TN smashrate; schtasks /Run /TN smashrate` で再起動する。
起動がブロックされる場合は Windows セキュリティの Smart App Control をオフにする。

以下は公開（外から使う）ための追加手順。

# 公開手順: Cloudflare Tunnel + Access + 簡易ログイン

ミニPC上の `smashrate serve`（localhost:8080）を、ポート開放なしで `https://smash.example.com` として公開し、
Cloudflare Access でメンバーだけに絞り、さらにアプリ側のログインで記録・削除を守る構成。追加費用はゼロ
（ドメインは既存のものを Cloudflare に載せてある前提）。

## 1. アプリ側の設定（smashrate.toml）

```toml
[server]
addr = "127.0.0.1:8080"          # LAN からの直接アクセスを閉じ、トンネル経由だけにする
base_url = "https://smash.example.com"

[server.auth]
password = "長めのパスワード"        # 空なら無効。記録・編集・削除に必要
api_token = "ランダムな文字列"       # スクリプトから API を叩くとき用（任意）
read_requires_login = false       # true にすると閲覧にもログインが要る
trust_cloudflare_access = true    # Access でサインイン済みの人はログイン不要にする
```

`trust_cloudflare_access` は、Access を通ったリクエストに Cloudflare が付ける
`Cf-Access-Authenticated-User-Email` ヘッダを信用する設定です。`addr` を `127.0.0.1:8080` にして
トンネル以外から届かないようにしてから有効にしてください（LAN から直接叩けるとヘッダを偽装できるため）。
家の中からも `https://smash.example.com` で開けば同じ経路になります。

## 2. cloudflared

既に別のミニPCで使っているアカウントをそのまま使います。トンネルはアカウント内に何本でも作れます。

```powershell
winget install Cloudflare.cloudflared
cloudflared tunnel login                     # ブラウザでドメインを選ぶ
cloudflared tunnel create smashrate          # <TUNNEL-ID> と credentials JSON ができる
cloudflared tunnel route dns smashrate smash.example.com
```

`deploy\cloudflared.config.yml` を `%USERPROFILE%\.cloudflared\config.yml` にコピーして
`<TUNNEL-ID>` とホスト名を書き換え、動作確認してからサービス化します。

```powershell
cloudflared tunnel run smashrate             # 動作確認
cloudflared service install                  # Windows サービスとして常駐
```

## 3. Access でメンバー制限

Cloudflare ダッシュボード → Zero Trust → Access → Applications → Add an application → Self-hosted。

- Application domain: `smash.example.com`
- Identity providers: One-time PIN（メール）だけでも十分。Google ログインも無料。
- Policy: Allow, Include → Emails（メンバーのアドレスを列挙）か Email domain。

これで URL を開くとまず Cloudflare のサインイン画面が出て、通った人だけがアプリに到達します。
Slack の通知リンクも `base_url` が https になっているのでそのまま踏めます。

## 4. smashrate の常駐とバックアップ

```powershell
go build -o smashrate.exe ./cmd/smashrate
powershell -ExecutionPolicy Bypass -File deploy\install-task.ps1    # ログオン時に serve、落ちたら再起動
schtasks /Create /TN smashrate-backup /SC DAILY /ST 04:00 /TR "powershell -ExecutionPolicy Bypass -File C:\path\to\deploy\backup.ps1"
```

## 5. 確認

1. `https://smash.example.com/healthz` が `ok` を返す（Access のサインイン後）。
2. ランキングが見える。右上に「ログイン」が出る（`trust_cloudflare_access = true` なら出ない）。
3. 手動記録を試し、ログインを求められる／Access 経由なら通ることを確認。
4. `curl -H "Authorization: Bearer <api_token>" -X POST https://smash.example.com/api/matches -d '{...}'` が 201 を返す。

## 守られ方のまとめ

| 経路 | 閲覧 | 記録・削除 |
|---|---|---|
| トンネル経由・Access サインイン済み | 可 | 可（`trust_cloudflare_access` が true のとき。false ならパスワード） |
| トンネル経由・未サインイン | Access が遮断 | Access が遮断 |
| LAN から 8080 直接 | `addr` を 127.0.0.1 にしていれば不可 | 不可 |
| スクリプト（API） | 可 | `Authorization: Bearer <api_token>` |
